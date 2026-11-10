package installation

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
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gofrs/flock"
	"golang.org/x/oauth2"

	"github.com/holon-sh/cli/pkg/installation/installationtest"
	"github.com/holon-sh/protocol"
)

func newAuthTestServer(t *testing.T, tokenHandler http.HandlerFunc) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	var server *httptest.Server
	installationtest.MountDeviceAuth(t, mux, func() string { return server.URL + "/" }, protocol.AdminPlane, nil, 120, 1, tokenHandler)
	server = httptest.NewTLSServer(mux)
	t.Cleanup(server.Close)
	return server
}

func TestLogin(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, bearer string
		down         bool
	}{
		{name: "identity provider", bearer: "Bearer access-secret"},
		{name: "identity provider unreachable at logout", bearer: "Bearer access-secret", down: true},
		{name: "authentication disabled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mux := http.NewServeMux()
			var server *httptest.Server
			var revoked []string
			if tc.bearer == "" {
				mux.HandleFunc(protocol.APIPathInfo, func(w http.ResponseWriter, r *http.Request) {
					installationtest.WriteJSON(w, protocol.Info{Installation: protocol.InstallationReference{Origin: "https://" + r.Host, UID: "test-installation"}})
				})
			} else {
				installationtest.MountDeviceAuth(t, mux, func() string { return server.URL + "/" }, protocol.AdminPlane, func(r *http.Request) {
					if r.URL.Path == "/revoke" {
						revoked = append(revoked, r.FormValue("token"))
					}
				}, 120, 1, func(w http.ResponseWriter, _ *http.Request) {
					installationtest.WriteJSON(w, map[string]any{"token_type": "Bearer", "access_token": "access-secret", "refresh_token": "refresh-one", "expires_in": 120})
				})
			}
			installationtest.NewControlFixtures(t, mux).Handle([]string{protocol.ToolDeleteValue}, nil, func(w http.ResponseWriter, r *http.Request) {
				if got := r.Header.Get(protocol.HeaderAuthorization); got != tc.bearer {
					t.Errorf("authorization = %q, want %q", got, tc.bearer)
				}
				installationtest.WriteJSON(w, struct{}{})
			})
			server = httptest.NewTLSServer(mux)
			t.Cleanup(server.Close)
			config := commandConfig{Auth: ClientConfig{ConfigDir: t.TempDir(), HTTPClient: installationtest.OriginClient(server)}}
			var output, progress strings.Builder
			for _, args := range [][]string{{"login", server.URL}, {"login"}, {"config", "delete", "REGION", "--env", "prod"}} {
				if err := executeCommand(t.Context(), args, &output, &progress, config); err != nil {
					t.Fatal(err)
				}
			}
			if !strings.HasPrefix(output.String(), "Deleted config REGION in production") {
				t.Fatalf("stdout = %q", output.String())
			}
			if tc.bearer != "" && !strings.Contains(progress.String(), "ABCD-EFGH") {
				t.Fatalf("device instructions: %q", progress.String())
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
			if tc.down {
				server.Close()
			}
			err = executeCommand(t.Context(), []string{"logout"}, nil, nil, config)
			if tc.down != (err != nil) {
				t.Fatalf("logout error = %v, want error %v", err, tc.down)
			}
			if _, err := os.Stat(files[0]); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("logout retained tokens: %v", err)
			}
			if tc.bearer != "" && !tc.down && !slices.Equal(revoked, []string{"refresh-one"}) {
				t.Fatalf("revoked tokens = %v", revoked)
			}
			assertSavedOrigin(t, config.Auth.ConfigDir, server.URL)
		})
	}
}

func TestTerminalSignInLeavesOtherCommandsFree(t *testing.T) {
	t.Parallel()
	entered, release := make(chan struct{}), make(chan struct{})
	server := newAuthTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		installationtest.WriteJSON(w, map[string]any{"token_type": "Bearer", "access_token": "access-secret", "refresh_token": "refresh-secret", "expires_in": 120})
	})
	dir := t.TempDir()
	signingIn := newManager(t, server, dir)
	signingIn.interactive = true
	done := make(chan error, 1)
	go func() {
		token, err := signingIn.controlToken(t.Context())
		if err == nil && token != "access-secret" {
			err = fmt.Errorf("token %q", token)
		}
		done <- err
	}()
	<-entered
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if _, err := newManager(t, server, dir).controlToken(ctx); !errors.Is(err, errNotAuthenticated) {
		t.Fatalf("while another command signs in: %v, want %v", err, errNotAuthenticated)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if token, err := newManager(t, server, dir).controlToken(ctx); err != nil || token != "access-secret" {
		t.Fatalf("after sign-in token=%q error=%v", token, err)
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
		{name: "initiation", code: "invalid_request", path: "/device-authorization", want: errOAuthFailed},
		{name: "wrong issuer", discovery: `{"issuer":"https://foreign.example/"}`, want: errOAuthFailed},
		{
			name: "missing device endpoint", discovery: `{"issuer":"https://identity.example/","token_endpoint":"https://identity.example/token"}`, want: errOAuthFailed,
		},
		{
			name:      "insecure token endpoint",
			discovery: `{"issuer":"https://identity.example/","token_endpoint":"http://identity.example/token"}`, want: errMalformedResponse,
		},
		{name: "insecure device endpoint", discovery: `{"issuer":"https://identity.example/","token_endpoint":"https://identity.example/token",` +
			`"device_authorization_endpoint":"http://identity.example/device"}`, want: errMalformedResponse},
		{name: "insecure revocation endpoint", discovery: `{"issuer":"https://identity.example/","token_endpoint":"https://identity.example/token",` +
			`"revocation_endpoint":"http://identity.example/revoke"}`, want: errMalformedResponse},
		{name: "denied", code: "access_denied", want: errDeviceDenied},
		{name: "expired", code: "authorization_pending", expires: 2, want: errDeviceExpired},
		{name: "cancelled", code: "authorization_pending", cancel: true, want: context.Canceled},
	} {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				mux := http.NewServeMux()
				fail := func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set(protocol.HeaderContentType, protocol.MIMEApplicationJSON)
					w.WriteHeader(http.StatusBadRequest)
					installationtest.WriteJSON(w, map[string]string{"error": test.code, "error_description": "refresh-secret"})
					if test.cancel {
						cancel()
					}
				}
				installationtest.MountDeviceAuth(t, mux, func() string { return "https://identity.example/" }, protocol.AdminPlane, nil, test.expires, 1, fail)
				client := installationtest.HTTPClient(func(request *http.Request) (*http.Response, error) {
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
		name, code           string
		status               int
		refreshErr, laterErr error
	}{
		{name: "temporary failure", code: "temporary_failure", status: http.StatusServiceUnavailable, refreshErr: errRefreshFailed},
		{name: "revoked credential", code: "invalid_grant", status: http.StatusBadRequest, refreshErr: errReauthenticationRequired, laterErr: errReauthenticationRequired},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
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
						installationtest.WriteJSON(w, map[string]string{"error": tc.code})
						return
					}
					installationtest.WriteJSON(w, map[string]any{"token_type": "Bearer", "access_token": "recovered-access", "expires_in": 120})
					return
				}
				installationtest.WriteJSON(w, map[string]any{"token_type": "Bearer", "access_token": "access-secret", "refresh_token": "refresh-one"})
			})
			first := newManager(t, server, dir)
			if err := first.loginInteractive(t.Context()); err != nil {
				t.Fatal(err)
			}
			if _, err := first.controlToken(t.Context()); !errors.Is(err, tc.refreshErr) {
				t.Fatalf("refresh error=%v, want %v", err, tc.refreshErr)
			}

			for _, manager := range []*Client{newManager(t, server, dir), first} {
				if token, err := manager.controlToken(t.Context()); !errors.Is(err, tc.laterErr) || err == nil && token != "recovered-access" {
					t.Fatalf("later token=%q error=%v, want %v", token, err, tc.laterErr)
				}
			}
		})
	}
}

func newManager(t *testing.T, server *httptest.Server, path string) *Client {
	t.Helper()
	manager, err := NewClient(t.Context(), ClientConfig{
		Origin: server.URL, ConfigDir: path, HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

func TestLoginRejectsIncompatibleDiscovery(t *testing.T) {
	t.Parallel()
	const installation = `"installation":{"origin":"ORIGIN","uid":"test-installation"},`
	for _, body := range []string{
		`{"installation":{"origin":"https://other.example","uid":"installation"},"oauth":null}`,
		`{` + installation + `"oauth":null,"buildArchitectures":["s390x"]}`,
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
				_, _ = io.WriteString(w, strings.NewReplacer("ORIGIN", origin, "ADMIN", installationtest.AdminAudience(origin)).Replace(body))
			}))
			t.Cleanup(server.Close)
			config := commandConfig{Auth: ClientConfig{ConfigDir: t.TempDir(), HTTPClient: installationtest.OriginClient(server)}}
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
	server := newAuthTestServer(t, nil)
	for _, tc := range []struct {
		name, document string
		want           error
	}{
		{"incomplete", `{"issuer":"ISSUER"}`, errReauthenticationRequired},
		{"changed issuer", `{"issuer":"https://other.example/","adminAudience":"urn:test","cliClientID":"cli-test"}`, errEndpointChanged},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager := newManager(t, server, t.TempDir())
			seedAdminCredentials(t, manager, oauth2.Token{
				AccessToken: "access-token", RefreshToken: "refresh-token",
				TokenType: "Bearer", ExpiresIn: 3600, Expiry: time.Now().Add(time.Hour),
			})
			tokens, _ := json.Marshal(manager.state.Tokens)
			oauth := strings.NewReplacer("ISSUER", server.URL+"/", "urn:test", installationtest.AdminAudience(server.URL)).Replace(tc.document)
			data := fmt.Sprintf(`{"version":"test","tokens":%s,"oauth":%s}`, tokens, oauth)
			if err := writePrivateFile(manager.statePath, []byte(data)); err != nil {
				t.Fatal(err)
			}
			token, err := manager.controlToken(t.Context())
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
			HTTPClient: installationtest.OriginClient(server), ConfigDir: t.TempDir(),
		},
	}
}

func TestMachineCredentials(t *testing.T) {
	t.Parallel()
	for _, scenario := range []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"valid", 200, `{"access_token":"machine-token","token_type":"Bearer"}`, nil},
		{"invalid secret", 401, `{"error":"invalid_client","error_description":"ci-secret"}`, errOAuthFailed},
		{"invalid token", 200, `{"access_token":"invalid token ci-secret","token_type":"Bearer"}`, errMalformedResponse},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			t.Parallel()
			server := installationtest.MachineAuthServer(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set(protocol.HeaderContentType, protocol.MIMEApplicationJSON)
				w.WriteHeader(scenario.status)
				_, _ = io.WriteString(w, scenario.body)
			})
			installationtest.NewControlFixtures(t, server.Config.Handler.(*http.ServeMux)).Handle([]string{protocol.ToolDeleteValue}, nil, func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get(protocol.HeaderAuthorization) != "Bearer machine-token" {
					t.Error("machine token missing")
				}
				installationtest.WriteJSON(w, struct{}{})
			})
			config := machineTestConfig(t, server)
			var output strings.Builder
			err := executeCommand(t.Context(), []string{"config", "delete", "REGION", "--env", "prod"}, &output, &output, config)
			if !errors.Is(err, scenario.want) || strings.Contains(fmt.Sprint(err)+output.String(), "ci-secret") || strings.Contains(output.String(), "machine-token") {
				t.Fatalf("error = %v, output = %q", err, output.String())
			}
			if files, err := os.ReadDir(config.Auth.ConfigDir); err != nil || len(files) != 0 {
				t.Fatalf("machine authentication wrote local credentials: files=%v error=%v", files, err)
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
		installationtest.WriteJSON(w, protocol.Info{Installation: protocol.InstallationReference{Origin: "https://" + r.Host, UID: "test-installation"}})
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

func TestConcurrentAuthenticationProcesses(t *testing.T) {
	if os.Getenv("HOLON_AUTH_HELPER") == "1" {
		roots := x509.NewCertPool()
		roots.AppendCertsFromPEM([]byte(os.Getenv("HOLON_AUTH_CERT")))
		dir := os.Getenv("HOLON_AUTH_DIR")
		manager, err := NewClient(t.Context(), ClientConfig{
			Origin: os.Getenv("HOLON_AUTH_REMOTE"), ConfigDir: dir,
			HTTPClient: &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}},
		})
		if err != nil {
			t.Fatal(err)
		}
		token, err := manager.controlToken(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if token != "shared-access" {
			t.Fatalf("unexpected token %q", token)
		}
		return
	}

	t.Parallel()
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
		if r.FormValue("grant_type") != "refresh_token" || r.FormValue("refresh_token") != "refresh-one" {
			t.Error("replayed a rotated refresh token")
		}
		if first {
			close(entered)
		}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		installationtest.WriteJSON(w, map[string]any{"access_token": "shared-access", "token_type": "Bearer", "refresh_token": "refresh-two", "expires_in": 3600})
	})
	base := server.Config.Handler
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == protocol.APIPathInfo {
			info <- struct{}{}
		}
		base.ServeHTTP(w, r)
	})
	dir := t.TempDir()
	manager := newManager(t, server, dir)
	seedAdminCredentials(t, manager, oauth2.Token{
		AccessToken: "expired", RefreshToken: "refresh-one",
		TokenType: "Bearer", Expiry: time.Unix(1, 0),
	})
	<-info
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
		installationtest.WriteJSON(w, map[string]any{"access_token": "other-access", "token_type": "Bearer", "refresh_token": "other-refresh", "expires_in": 3600})
	})
	otherManager := newManager(t, other, dir)
	if err := otherManager.loginInteractive(ctx); err != nil {
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
}

func TestAuthenticationWaitCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		config := ClientConfig{Origin: "https://one.example", ConfigDir: t.TempDir()}
		manager, err := newLocalAuthManager(config)
		if err != nil {
			t.Fatal(err)
		}
		manager.info.OAuth = &protocol.OAuthInfo{Issuer: "https://identity.example", AdminAudience: installationtest.AdminAudience(config.Origin), CLIClientID: "cli"}
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
		go func() { _, err := manager.controlToken(ctx); done <- err }()
		synctest.Wait()
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled connection error=%v", err)
		}
	})
}

func TestResourceTokenCacheIsPerAudience(t *testing.T) {
	var requests []string
	server := installationtest.MachineAuthServer(t, func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.FormValue("resource")+"|"+r.FormValue("scope"))
		installationtest.WriteJSON(w, map[string]any{"access_token": fmt.Sprintf("token-%d", len(requests)), "token_type": "Bearer", "expires_in": 3600})
	})
	config := machineTestConfig(t, server)
	config.Auth.Env = config.Env
	manager, err := NewClient(t.Context(), config.Auth)
	if err != nil {
		t.Fatal(err)
	}
	audience := installationtest.AdminAudience(server.URL)
	token, err := manager.controlToken(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	again, err := manager.controlToken(t.Context())
	if err != nil || again != token || len(requests) != 1 {
		t.Fatalf("control token cache miss: %v %v", requests, err)
	}
	if requests[0] != audience+"|" {
		t.Fatalf("wrong control token request: %s", requests[0])
	}
	other, err := manager.resourceToken(t.Context(), audience+"/other")
	if err != nil || other.AccessToken == token || len(requests) != 2 {
		t.Fatalf("audience cache collision: %v %v", requests, err)
	}
}

func seedAdminCredentials(t *testing.T, manager *Client, token oauth2.Token) {
	t.Helper()
	manager.state = authState{OAuth: targetForOAuth(manager.info.OAuth), Version: "test"}
	manager.state.Tokens = map[string]*oauth2.Token{manager.info.OAuth.AdminAudience: &token}
	if err := manager.withLock(t.Context(), func() error { return manager.writeState(manager.state) }); err != nil {
		t.Fatal(err)
	}
}
