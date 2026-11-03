package cloud

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gofrs/flock"
	"golang.org/x/oauth2"
	testingclock "k8s.io/utils/clock/testing"

	"github.com/holon-sh/cli/pkg/protocol"
	"github.com/holon-sh/cli/pkg/cloud/cloudtest"
)

func newAuthTestServer(t *testing.T, tokenHandler http.HandlerFunc) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	var server *httptest.Server
	cloudtest.MountDeviceAuth(t, mux, func() string { return server.URL + "/" }, protocol.AdminPlane, nil, 120, 1, tokenHandler)
	server = httptest.NewTLSServer(mux)
	t.Cleanup(server.Close)
	return server
}

func TestLogin(t *testing.T) {
	t.Parallel()
	server := newAuthTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		cloudtest.WriteJSON(w, map[string]any{"token_type": "Bearer", "access_token": "access-secret", "refresh_token": "refresh-one", "expires_in": 120})
	})
	control := cloudtest.NewControlFixtures(t, server.Config.Handler.(*http.ServeMux))
	control.Handle([]string{protocol.ToolDeleteValue}, nil, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(protocol.HeaderAuthorization) != "Bearer access-secret" {
			t.Error("authenticated command omitted its token")
		}
		cloudtest.WriteJSON(w, struct{}{})
	})
	config := commandConfig{
		Auth: ClientConfig{ConfigDir: t.TempDir(), HTTPClient: cloudtest.OriginClient(server)},
	}
	var output, progress strings.Builder
	if err := executeCommand(t.Context(), []string{"login", server.URL}, &output, &progress, config); err != nil {
		t.Fatal(err)
	}
	if err := executeCommand(t.Context(), []string{"config", "delete", "REGION", "--env", "prod"}, &output, &progress, config); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(output.String(), "Deleted config REGION in production") {
		t.Fatalf("authenticated output=%q", output.String())
	}
	if !strings.Contains(progress.String(), "ABCD-EFGH") || strings.Contains(output.String(), "Code:") {
		t.Fatalf("device instructions: stdout=%q stderr=%q", output.String(), progress.String())
	}
	for _, secret := range []string{"access-secret", "device-secret", "refresh-one"} {
		if strings.Contains(output.String()+progress.String(), secret) {
			t.Fatal("login exposed a credential")
		}
	}
	files, err := filepath.Glob(filepath.Join(config.Auth.ConfigDir, "remotes", "*.tokens.json"))
	if err != nil || len(files) != 1 {
		t.Fatalf("token files=%v error=%v", files, err)
	}
	for path, mode := range map[string]os.FileMode{files[0]: 0o600, filepath.Dir(files[0]): 0o700} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != mode {
			t.Fatalf("private storage mode for %s: %v %v", path, info, err)
		}
	}
	if err := executeCommand(t.Context(), []string{"logout"}, nil, &progress, config); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(files[0]); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("logout retained tokens: %v", err)
	}
	assertSavedOrigin(t, config.Auth.ConfigDir, server.URL)
}

func TestCredentialChanges(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, remote string
		relogin      bool
		want         error
	}{
		{name: "logout from another command", want: errNotAuthenticated},
		{name: "logout from another remote with the same identity provider", remote: "https://other.example"},
		{name: "reauthorization from another command", relogin: true, want: errCredentialChanged},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			server := newAuthTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
				cloudtest.WriteJSON(w, map[string]any{"token_type": "Bearer", "access_token": "access-secret", "refresh_token": "refresh-secret", "expires_in": 120})
			})
			config := ClientConfig{
				Origin:    server.URL,
				ConfigDir: t.TempDir(), HTTPClient: cloudtest.OriginClient(server),
			}
			manager, err := NewClient(t.Context(), config)
			if err != nil {
				t.Fatal(err)
			}
			if tc.remote != "" {
				config.Origin = tc.remote
			}
			if _, err := manager.controlToken(t.Context(), protocol.AdminPlane); err != nil {
				t.Fatal(err)
			}
			command := "logout"
			if tc.relogin {
				command = "login"
			}
			if err := executeCommand(t.Context(), []string{command}, nil, nil, commandConfig{Auth: config}); err != nil {
				t.Fatal(err)
			}
			if token, err := manager.controlToken(t.Context(), protocol.AdminPlane); !errors.Is(err, tc.want) || err == nil && token != "access-secret" {
				t.Fatalf("after credential change token=%q error=%v, want %v", token, err, tc.want)
			}
		})
	}
}

func TestDeviceLoginFailures(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, code, path, discovery string
		expires                     int64
		cancel                      bool
		want                        error
	}{
		{name: "initiation", code: "invalid_request", path: "/device-authorization", expires: 120, want: errOAuthFailed},
		{name: "poll", code: "refresh-secret", expires: 120, want: errOAuthFailed},
		{name: "discovery", path: "/.well-known/openid-configuration", expires: 120, want: errOAuthFailed},
		{name: "wrong issuer", discovery: `{"issuer":"https://foreign.example/"}`, want: errOAuthFailed},
		{name: "missing token endpoint", discovery: `{"issuer":"https://identity.example/"}`, want: errMalformedResponse},
		{
			name: "missing device endpoint", discovery: `{"issuer":"https://identity.example/","token_endpoint":"https://identity.example/token"}`, want: errOAuthFailed,
		},
		{
			name:      "insecure token endpoint",
			discovery: `{"issuer":"https://identity.example/","token_endpoint":"http://identity.example/token"}`, want: errMalformedResponse,
		},
		{name: "insecure device endpoint", discovery: `{"issuer":"https://identity.example/","token_endpoint":"https://identity.example/token",` +
			`"device_authorization_endpoint":"http://identity.example/device"}`, want: errMalformedResponse},
		{name: "denied", code: "access_denied", expires: 120, want: errDeviceDenied},
		{name: "expired", code: "authorization_pending", expires: 2, want: errDeviceExpired},
		{name: "cancelled", code: "authorization_pending", expires: 120, cancel: true, want: context.Canceled},
	} {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				mux := http.NewServeMux()
				fail := func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusBadRequest)
					cloudtest.WriteJSON(w, map[string]string{"error": test.code, "error_description": "refresh-secret"})
					if test.cancel {
						cancel()
					}
				}
				cloudtest.MountDeviceAuth(t, mux, func() string { return "https://identity.example/" }, protocol.AdminPlane, nil, test.expires, 1, fail)
				client := cloudtest.HTTPClient(func(request *http.Request) (*http.Response, error) {
					response := httptest.NewRecorder()
					if request.URL.Path == test.path {
						fail(response, request)
					} else if request.URL.Path == "/.well-known/openid-configuration" && test.discovery != "" {
						_, _ = io.WriteString(response, test.discovery)
					} else {
						mux.ServeHTTP(response, request)
					}
					return response.Result(), nil
				})
				path := t.TempDir()
				previous := `{"oauth":null,"version":"previous"}`
				config := ClientConfig{
					Origin: "https://identity.example", ConfigDir: path, HTTPClient: client,
					RequestTimeout: time.Second,
				}
				manager, err := newLocalAuthManager(config)
				if err != nil {
					t.Fatal(err)
				}
				if err := writePrivateFile(manager.statePath, []byte(previous)); err != nil {
					t.Fatal(err)
				}
				sourceFiles(t, path, map[string]string{"config.json": `{"origin":"https://previous.example"}`})
				err = executeCommand(ctx, []string{"login", "https://identity.example"}, nil, nil, commandConfig{Auth: config})
				if !errors.Is(err, test.want) || strings.Contains(err.Error(), "refresh-secret") {
					t.Fatalf("login error = %v, want %v", err, test.want)
				}
				assertSavedOrigin(t, path, "https://previous.example")
				if saved, err := os.ReadFile(manager.statePath); err != nil || string(saved) != previous {
					t.Fatalf("failed login changed existing authentication state: %v", err)
				}
			})
		})
	}
}

func TestRefreshRecovery(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, code                       string
		status                           int
		failStore                        bool
		refreshErr, connectErr, olderErr error
	}{
		{name: "temporary failure", code: "temporary_failure", status: http.StatusServiceUnavailable, refreshErr: errRefreshFailed},
		{name: "revoked credential", code: "invalid_grant", status: http.StatusBadRequest, refreshErr: errReauthenticationRequired, olderErr: errCredentialChanged},
		{
			name: "replacement cannot be saved", code: "invalid_grant", status: http.StatusBadRequest, failStore: true,
			refreshErr: errReauthenticationRequired, connectErr: errCredentialUnavailable, olderErr: errReauthenticationRequired,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			clock := testingclock.NewFakePassiveClock(time.Unix(100, 0))
			dir := t.TempDir()
			var failed atomic.Bool
			server := newAuthTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				if r.FormValue("grant_type") == "refresh_token" {
					if r.FormValue("refresh_token") != "refresh-one" {
						t.Error("retry lost the usable refresh credential")
					}
					if !failed.Swap(true) || tc.code == "invalid_grant" {
						w.Header().Set(protocol.HeaderContentType, protocol.MIMEApplicationJSON)
						w.WriteHeader(tc.status)
						cloudtest.WriteJSON(w, map[string]string{"error": tc.code})
						return
					}
					cloudtest.WriteJSON(w, map[string]any{"token_type": "Bearer", "access_token": "recovered-access", "expires_in": 120})
					return
				}
				access, lifetime := "access-secret", 10
				if failed.Load() {
					access, lifetime = "recovered-access", 120
					if tc.failStore {
						if err := os.Chmod(filepath.Join(dir, "remotes"), 0o500); err != nil {
							t.Error(err)
						}
					}
				}
				cloudtest.WriteJSON(w, map[string]any{"token_type": "Bearer", "access_token": access, "refresh_token": "refresh-one", "expires_in": lifetime})
			})
			first := newManager(t, server, clock, dir)
			if err := first.loginInteractive(t.Context()); err != nil {
				t.Fatal(err)
			}
			clock.SetTime(clock.Now().Add(100 * time.Second))
			if _, err := first.controlToken(t.Context(), protocol.AdminPlane); !errors.Is(err, tc.refreshErr) {
				t.Fatalf("refresh error=%v, want %v", err, tc.refreshErr)
			}

			second := newManager(t, server, clock, dir)
			token, err := second.controlToken(t.Context(), protocol.AdminPlane)
			if !errors.Is(err, tc.connectErr) || err == nil && token != "recovered-access" {
				t.Fatalf("reconnect token=%q error=%v, want %v", token, err, tc.connectErr)
			}
			if err := os.Chmod(filepath.Join(dir, "remotes"), 0o700); err != nil {
				t.Fatal(err)
			}
			if token, err := first.controlToken(t.Context(), protocol.AdminPlane); !errors.Is(err, tc.olderErr) || err == nil && token != "recovered-access" {
				t.Fatalf("older command token=%q error=%v, want %v", token, err, tc.olderErr)
			}
		})
	}
}

func newManager(t *testing.T, server *httptest.Server, clock *testingclock.FakePassiveClock, path string) *Client {
	t.Helper()
	manager, err := NewClient(t.Context(), ClientConfig{
		Origin: server.URL, ConfigDir: path, HTTPClient: server.Client(), now: clock.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

func TestAnonymousConnectionAndLogout(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mux := http.NewServeMux()
	mux.HandleFunc(protocol.APIPathInfo, func(w http.ResponseWriter, r *http.Request) {
		cloudtest.WriteJSON(w, protocol.Info{Installation: protocol.InstallationReference{Origin: "https://" + r.Host, UID: "test-installation"}, Version: "test"})
	})
	cloudtest.NewControlFixtures(t, mux).Handle([]string{protocol.ToolDeleteValue}, nil, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(protocol.HeaderAuthorization) != "" {
			t.Error("anonymous bearer sent")
		}
		cloudtest.WriteJSON(w, struct{}{})
	})
	server := httptest.NewTLSServer(mux)
	t.Cleanup(server.Close)
	config := commandConfig{
		Env:  []string{"HOLON_ORIGIN=invalid"},
		Auth: ClientConfig{ConfigDir: t.TempDir(), HTTPClient: cloudtest.OriginClient(server)},
	}
	for _, args := range [][]string{{"login", server.URL}, {"login"}} {
		if err := executeCommand(t.Context(), args, nil, nil, config); err != nil {
			t.Fatal(err)
		}
		assertSavedOrigin(t, config.Auth.ConfigDir, server.URL)
		config.Env = nil
	}
	var output strings.Builder
	if err := executeCommand(t.Context(), []string{"config", "delete", "REGION", "--env", "prod"}, &output, nil, config); err != nil ||
		!strings.HasPrefix(output.String(), "Deleted config REGION in production") {
		t.Fatalf("anonymous output = %q, %v", output.String(), err)
	}
	t.Run("write failure", func(t *testing.T) {
		config := config
		config.Auth.ConfigDir = t.TempDir()
		if err := os.Mkdir(filepath.Join(config.Auth.ConfigDir, "config.json"), 0o700); err != nil {
			t.Fatal(err)
		}
		err := executeCommand(t.Context(), []string{"login", server.URL}, nil, nil, config)
		if err == nil || !strings.Contains(err.Error(), "save Holon configuration") {
			t.Fatalf("failed save: %v", err)
		}
	})
	server.Close()
	if err := executeCommand(t.Context(), []string{"logout"}, nil, nil, config); err != nil {
		t.Fatal(err)
	}
	assertSavedOrigin(t, config.Auth.ConfigDir, server.URL)
}

func TestLoginRejectsIncompatibleDiscovery(t *testing.T) {
	t.Parallel()
	const installation = `"installation":{"origin":"ORIGIN","uid":"test-installation"},`
	for _, body := range []string{
		`{` + installation + `"version":"test"}`,
		`{"installation":{"origin":"ORIGIN"},"oauth":null}`,
		`{"installation":{"origin":"ORIGIN","uid":"invalid uid"},"oauth":null}`,
		`{"installation":{"origin":"https://other.example","uid":"installation"},"oauth":null}`,
		`{` + installation + `"oauth":{}}`, `{` + installation + `"oauth":true}`,
		`{` + installation + `"oauth":null,"buildArchitectures":["s390x"]}`,
		`{` + installation + `"oauth":null,"buildArchitectures":["arm64","arm64"]}`,
		`{` + installation + `"oauth":{"issuer":"ORIGIN/","cliClientID":"cli","adminAudience":"ADMIN","deviceFlowProfile":"unknown"}}`,
	} {
		t.Run(body, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != protocol.APIPathInfo {
					t.Errorf("incompatible discovery triggered request to %s", r.URL.Path)
					http.NotFound(w, r)
					return
				}
				origin := "https://" + r.Host
				_, _ = io.WriteString(w, strings.NewReplacer("ORIGIN", origin, "ADMIN", cloudtest.AdminAudience(origin)).Replace(body))
			}))
			t.Cleanup(server.Close)
			config := commandConfig{Auth: ClientConfig{ConfigDir: t.TempDir(), HTTPClient: cloudtest.OriginClient(server)}}
			sourceFiles(t, config.Auth.ConfigDir, map[string]string{"config.json": `{"origin":"https://previous.example"}`})
			if err := executeCommand(t.Context(), []string{"login", server.URL}, nil, nil, config); !errors.Is(err, errMalformedResponse) {
				t.Fatalf("incompatible discovery: %v", err)
			}
			assertSavedOrigin(t, config.Auth.ConfigDir, "https://previous.example")
		})
	}
}

func TestSavedCredentialMetadata(t *testing.T) {
	t.Parallel()
	clock := testingclock.NewFakePassiveClock(time.Unix(100, 0))
	server := newAuthTestServer(t, nil)
	for _, tc := range []struct {
		name, document string
		want           error
	}{
		{"matching", `{"issuer":"ISSUER","adminAudience":"urn:test","cliClientID":"cli-test"}`, nil},
		{"incomplete", `{"issuer":"ISSUER"}`, errReauthenticationRequired},
		{"changed mode", `null`, errEndpointChanged},
		{"changed issuer", `{"issuer":"https://other.example/","adminAudience":"urn:test","cliClientID":"cli-test"}`, errEndpointChanged},
		{"changed audience", `{"issuer":"ISSUER","adminAudience":"urn:other","cliClientID":"cli-test"}`, errEndpointChanged},
		{"changed client", `{"issuer":"ISSUER","adminAudience":"urn:test","cliClientID":"other"}`, errEndpointChanged},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager := newManager(t, server, clock, t.TempDir())
			seedAdminCredentials(t, manager, oauth2.Token{
				AccessToken: "access-token", RefreshToken: "refresh-token",
				TokenType: "Bearer", ExpiresIn: 3600, Expiry: clock.Now().Add(time.Hour),
			})
			tokens, _ := json.Marshal(manager.state.Tokens)
			oauth := strings.NewReplacer("ISSUER", server.URL+"/", "urn:test", cloudtest.AdminAudience(server.URL)).Replace(tc.document)
			data := fmt.Sprintf(`{"version":"test","tokens":%s,"oauth":%s}`, tokens, oauth)
			if err := writePrivateFile(manager.statePath, []byte(data)); err != nil {
				t.Fatal(err)
			}
			token, err := manager.controlToken(t.Context(), protocol.AdminPlane)
			if !errors.Is(err, tc.want) || err == nil && token != "access-token" {
				t.Fatalf("token=%q error=%v, want %v", token, err, tc.want)
			}
		})
	}
}

func machineTestConfig(t *testing.T, server *httptest.Server) commandConfig {
	t.Helper()
	return commandConfig{
		Env: []string{"HOLON_ORIGIN=" + server.URL, "HOLON_CLIENT_ID=ci-deployer", "HOLON_CLIENT_SECRET=ci-secret"},
		Auth: ClientConfig{
			HTTPClient: cloudtest.OriginClient(server), ConfigDir: t.TempDir(),
		},
	}
}

func TestMachineCredentialsFailClosed(t *testing.T) {
	t.Parallel()
	for _, scenario := range []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"invalid secret", 401, `{"error":"invalid_client","error_description":"ci-secret"}`, errOAuthFailed},
		{"malformed response", 200, `{"access_token":"ci-secret","token_type":"Bearer","expires_in":0}`, errMalformedResponse},
		{"invalid token type", 200, `{"access_token":"ci-secret","token_type":"MAC","expires_in":120}`, errMalformedResponse},
		{"invalid token", 200, `{"access_token":"invalid token ci-secret","token_type":"Bearer","expires_in":120}`, errMalformedResponse},
		{"oversized lifetime", 200, `{"access_token":"ci-secret","token_type":"Bearer","expires_in":999999}`, errMalformedResponse},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			server := cloudtest.MachineAuthServer(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(scenario.status)
				_, _ = io.WriteString(w, scenario.body)
			})
			err := executeCommand(t.Context(), []string{"config", "delete", "REGION", "--env", "prod"}, nil, nil, machineTestConfig(t, server))
			if !errors.Is(err, scenario.want) ||
				strings.Contains(err.Error(), "ci-secret") {
				t.Fatalf("unsafe failure = %v", err)
			}
		})
	}
	for _, env := range [][]string{
		{"HOLON_CLIENT_ID=ci-deployer"},
		{"HOLON_CLIENT_SECRET=ci-secret"},
		{"HOLON_CLIENT_ID=", "HOLON_CLIENT_SECRET="},
	} {
		config := commandConfig{Env: env, Auth: ClientConfig{Origin: "https://unused.example", ConfigDir: t.TempDir()}}
		if err := executeCommand(t.Context(), []string{"config", "delete", "REGION", "--env", "prod"}, nil, nil, config); err == nil ||
			!strings.Contains(err.Error(), "requires HOLON_CLIENT_ID and HOLON_CLIENT_SECRET together") {
			t.Fatalf("incomplete credentials fell back: %v", err)
		}
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cloudtest.WriteJSON(w, protocol.Info{Installation: protocol.InstallationReference{Origin: "https://" + r.Host, UID: "test-installation"}})
	}))
	defer server.Close()
	if err := executeCommand(t.Context(), []string{"config", "delete", "REGION", "--env", "prod"}, nil, nil, machineTestConfig(t, server)); err == nil ||
		!strings.Contains(err.Error(), "requires an identity-enabled installation") {
		t.Fatalf("machine authentication on anonymous installation: %v", err)
	}
	if err := executeCommand(t.Context(), []string{"logout"}, nil, nil, machineTestConfig(t, server)); err == nil ||
		!strings.Contains(err.Error(), "service-account credentials are supplied by the environment") {
		t.Fatalf("machine logout = %v", err)
	}
}

func TestMachineCommandEnvironment(t *testing.T) {
	t.Parallel()
	var server *httptest.Server
	server = cloudtest.MachineAuthServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.FormValue("resource") != cloudtest.AdminAudience(server.URL) || r.FormValue("audience") != "" {
			t.Error("incorrect machine token audience")
		}
		cloudtest.WriteJSON(w, map[string]any{"access_token": "machine-token", "token_type": "Bearer", "expires_in": 120})
	})
	control := cloudtest.NewControlFixtures(t, server.Config.Handler.(*http.ServeMux))
	control.Handle([]string{protocol.ToolDeleteValue}, nil, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer machine-token" {
			t.Error("machine token missing")
		}
		cloudtest.WriteJSON(w, struct{}{})
	})
	config := machineTestConfig(t, server)
	var output strings.Builder
	if err := executeCommand(t.Context(), []string{"config", "delete", "REGION", "--env", "prod"}, &output, &output, config); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Deleted config REGION in production") ||
		strings.Contains(output.String(), "ci-secret") || strings.Contains(output.String(), "machine-token") {
		t.Fatalf("unexpected command output: %s", &output)
	}
	if files, err := os.ReadDir(config.Auth.ConfigDir); err != nil || len(files) != 0 {
		t.Fatalf("machine authentication wrote local credentials: files=%v error=%v", files, err)
	}
}

func TestConcurrentAuthenticationProcesses(t *testing.T) {
	if os.Getenv("HOLON_AUTH_HELPER") == "1" {
		roots := x509.NewCertPool()
		roots.AppendCertsFromPEM([]byte(os.Getenv("HOLON_AUTH_CERT")))
		dir := os.Getenv("HOLON_AUTH_DIR")
		manager, err := NewClient(t.Context(), ClientConfig{
			Origin: os.Getenv("HOLON_AUTH_REMOTE"), ConfigDir: dir,
			now:        func() time.Time { return time.Unix(100, 0) },
			HTTPClient: &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}},
		})
		if err != nil {
			t.Fatal(err)
		}
		token, err := manager.controlToken(t.Context(), protocol.AdminPlane)
		if err != nil {
			t.Fatal(err)
		}
		if token != "shared-access" {
			t.Fatalf("unexpected token %q", token)
		}
		return
	}

	t.Parallel()
	for _, refresh := range []bool{false, true} {
		name := "initial sign-in"
		if refresh {
			name = "rotating refresh"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			info := make(chan struct{}, 3)
			var authorizations atomic.Int32
			server := newAuthTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				first := authorizations.Add(1) == 1
				if refresh && (r.FormValue("grant_type") != "refresh_token" || r.FormValue("refresh_token") != "refresh-one") {
					t.Error("replayed a rotated refresh token")
				}
				if !refresh && r.FormValue("grant_type") != "urn:ietf:params:oauth:grant-type:device_code" {
					t.Error("unexpected initial grant")
				}
				if first {
					close(entered)
				}
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
				cloudtest.WriteJSON(w, map[string]any{"access_token": "shared-access", "token_type": "Bearer", "refresh_token": "refresh-two", "expires_in": 3600})
			})
			base := server.Config.Handler
			server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == protocol.APIPathInfo {
					info <- struct{}{}
				}
				base.ServeHTTP(w, r)
			})
			dir := t.TempDir()
			if refresh {
				manager := newManager(t, server, testingclock.NewFakePassiveClock(time.Unix(100, 0)), dir)
				seedAdminCredentials(t, manager, oauth2.Token{
					AccessToken: "expired", RefreshToken: "refresh-one",
					TokenType: "Bearer", ExpiresIn: 3600, Expiry: time.Unix(1, 0),
				})
				<-info
			}
			cert := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}))
			start := func() <-chan error {
				cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestConcurrentAuthenticationProcesses$")
				cmd.Env = []string{"HOLON_AUTH_HELPER=1", "HOLON_AUTH_REMOTE=" + server.URL, "HOLON_AUTH_DIR=" + dir, "HOLON_AUTH_CERT=" + cert}
				done := make(chan error, 1)
				go func() {
					output, err := cmd.CombinedOutput()
					if err != nil {
						err = fmt.Errorf("authentication process: %w\n%s", err, output)
					}
					done <- err
				}()
				return done
			}
			first := start()
			select {
			case <-entered:
			case err := <-first:
				t.Fatalf("process exited before authentication: %v", err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			<-info
			other := newAuthTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
				cloudtest.WriteJSON(w, map[string]any{"access_token": "other-access", "token_type": "Bearer", "refresh_token": "other-refresh", "expires_in": 3600})
			})
			otherManager, err := NewClient(ctx, ClientConfig{
				Origin: other.URL, ConfigDir: dir, HTTPClient: other.Client(),
			})
			if err == nil {
				_, err = otherManager.controlToken(ctx, protocol.AdminPlane)
			}
			if err != nil {
				t.Fatalf("another remote's authentication was blocked: %v", err)
			}
			second := start()
			select {
			case <-info:
			case err := <-second:
				t.Fatalf("process exited before discovery: %v", err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			unblock()
			for _, done := range []<-chan error{first, second} {
				if err := <-done; err != nil {
					t.Fatal(err)
				}
			}
			if authorizations.Load() != 1 {
				t.Fatalf("concurrent processes exchanged credentials %d times", authorizations.Load())
			}
		})
	}
}

func TestAuthenticationWaitCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client := cloudtest.HTTPClient(func(request *http.Request) (*http.Response, error) {
			response := httptest.NewRecorder()
			cloudtest.WriteJSON(response, protocol.Info{Installation: protocol.InstallationReference{Origin: "https://" + request.Host, UID: "test-installation"}})
			return response.Result(), nil
		})
		config := ClientConfig{
			Origin:    "https://one.example",
			ConfigDir: t.TempDir(), HTTPClient: client,
		}
		manager, err := newLocalAuthManager(config)
		if err != nil {
			t.Fatal(err)
		}
		manager.info.OAuth = &protocol.OAuthInfo{Issuer: "https://identity.example", AdminAudience: cloudtest.AdminAudience(config.Origin), CLIClientID: "cli"}
		if err := os.MkdirAll(filepath.Dir(manager.statePath), 0o700); err != nil {
			t.Fatal(err)
		}
		lock := flock.New(manager.statePath + ".lock")
		if err := lock.Lock(); err != nil {
			t.Fatal(err)
		}
		defer lock.Unlock()
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan error, 1)
		go func() { _, err := manager.controlToken(ctx, protocol.AdminPlane); done <- err }()
		synctest.Wait()
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled connection error=%v", err)
		}
	})
}

func TestControlTokenAudienceAndScopeCache(t *testing.T) {
	var requests []string
	server := cloudtest.MachineAuthServer(t, func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.FormValue("resource")+"|"+r.FormValue("scope"))
		cloudtest.WriteJSON(w, map[string]any{"access_token": fmt.Sprintf("token-%d", len(requests)), "token_type": "Bearer", "expires_in": 3600})
	})
	config := machineTestConfig(t, server)
	config.Auth.Env = config.Env
	manager, err := NewClient(t.Context(), config.Auth)
	if err != nil {
		t.Fatal(err)
	}
	devAudience, _ := protocol.PlaneOrigin(server.URL, protocol.DevPlane, "")
	admin, err := manager.controlToken(t.Context(), protocol.AdminPlane)
	if err != nil {
		t.Fatal(err)
	}
	dev, err := manager.controlToken(t.Context(), protocol.DevPlane)
	if err != nil || dev == admin {
		t.Fatalf("control tokens not isolated: %v", err)
	}
	again, err := manager.controlToken(t.Context(), protocol.DevPlane)
	if err != nil || again != dev || len(requests) != 2 {
		t.Fatalf("dev cache miss: %v %v", requests, err)
	}
	if requests[1] != devAudience+"|system:dev:read system:dev:write" {
		t.Fatalf("wrong dev request: %s", requests[1])
	}
	narrow, err := manager.resourceToken(t.Context(), devAudience, []string{"system:dev:read"})
	if err != nil || narrow.AccessToken == dev || len(requests) != 3 {
		t.Fatalf("scope cache collision: %v %v", requests, err)
	}
}

func seedAdminCredentials(t *testing.T, manager *Client, token oauth2.Token) {
	t.Helper()
	manager.state = authState{OAuth: targetForOAuth(manager.info.OAuth), Version: "test"}
	manager.state.Tokens = map[string]*oauth2.Token{tokenKey(manager.info.OAuth.Issuer, manager.audience(protocol.AdminPlane), protocol.SystemScopes()): &token}
	if err := manager.withLock(t.Context(), func() error { return manager.writeState(manager.state) }); err != nil {
		t.Fatal(err)
	}
}
