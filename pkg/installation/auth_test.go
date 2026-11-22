package installation

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
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
	installationtest.MountDeviceAuth(t, mux, func() string { return server.URL + "/" }, nil, 120, 1, tokenHandler)
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
				installationtest.MountDeviceAuth(t, mux, func() string { return server.URL + "/" }, func(r *http.Request) {
					if r.URL.Path == "/revoke" {
						revoked = append(revoked, r.FormValue("token"))
					}
				}, 120, 1, func(w http.ResponseWriter, _ *http.Request) {
					installationtest.WriteJSON(w, map[string]any{"token_type": "Bearer", "access_token": "access-secret", "refresh_token": "refresh-one", "expires_in": 120})
				})
			}
			control := installationtest.NewControlFixtures(t, mux)
			control.Handle([]string{protocol.ToolDeleteValue}, nil, func(w http.ResponseWriter, r *http.Request) {
				if got := r.Header.Get(protocol.HeaderAuthorization); got != tc.bearer {
					t.Errorf("authorization = %q, want %q", got, tc.bearer)
				}
				installationtest.WriteJSON(w, struct{}{})
			})
			control.Handle([]string{"get_identity"}, nil, func(w http.ResponseWriter, _ *http.Request) {
				installationtest.WriteJSON(w, map[string]any{"identity": map[string]any{
					"issuer": "https://identity.example", "subject": "user-1", "scopes": []string{},
					"profile": map[string]any{"name": "Dev", "email": "dev@example.com"},
				}})
			})
			server = httptest.NewTLSServer(mux)
			t.Cleanup(server.Close)
			config := commandConfig{Auth: ClientConfig{ConfigDir: t.TempDir(), HTTPClient: installationtest.OriginClient(server)}}
			var output, progress strings.Builder
			for _, args := range [][]string{{"login", server.URL}, {"login"}, {"config", "delete", "chat", "REGION", "--env", "prod"}} {
				if err := executeCommand(t.Context(), args, &output, &progress, config); err != nil {
					t.Fatal(err)
				}
			}
			if tc.bearer != "" && !strings.Contains(progress.String(), "ABCD-EFGH") {
				t.Fatalf("device instructions: %q", progress.String())
			}
			signedIn := "Signed in to " + server.URL + " as dev@example.com\n"
			if tc.bearer == "" {
				signedIn = "Connected to " + server.URL + " (authentication disabled)\n"
			}
			if !strings.Contains(progress.String(), signedIn) {
				t.Fatalf("progress = %q, want %q", progress.String(), signedIn)
			}
			for _, secret := range []string{"access-secret", "device-secret", "refresh-one"} {
				if strings.Contains(output.String()+progress.String(), secret) {
					t.Fatal("login exposed a credential")
				}
			}
			tokens := filepath.Join(config.Auth.ConfigDir, "remotes", "*.tokens.json")
			files, _ := filepath.Glob(tokens)
			if tc.bearer == "" && len(files) != 0 || tc.bearer != "" && len(files) != 1 {
				t.Fatalf("token files=%v", files)
			}
			if tc.down {
				server.Close()
			}
			err := executeCommand(t.Context(), []string{"logout"}, nil, nil, config)
			if tc.down != (err != nil) {
				t.Fatalf("logout error = %v, want error %v", err, tc.down)
			}
			if files, _ := filepath.Glob(tokens); len(files) != 0 {
				t.Fatalf("logout retained tokens: %v", files)
			}
			if tc.bearer != "" && !tc.down && !slices.Equal(revoked, []string{"refresh-one"}) {
				t.Fatalf("revoked tokens = %v", revoked)
			}
			assertSavedOrigin(t, config.Auth.ConfigDir, server.URL)
		})
	}
}

func TestTerminalSignIn(t *testing.T) {
	t.Parallel()
	var once sync.Once
	entered, release := make(chan struct{}), make(chan struct{})
	server := newAuthTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.FormValue("refresh_token") == "revoked" {
			w.Header().Set(protocol.HeaderContentType, protocol.MIMEApplicationJSON)
			w.WriteHeader(http.StatusBadRequest)
			installationtest.WriteJSON(w, map[string]string{"error": "invalid_grant"})
			return
		}
		once.Do(func() { close(entered) })
		<-release
		installationtest.WriteJSON(w, map[string]any{"token_type": "Bearer", "access_token": "access-secret", "refresh_token": "refresh-secret", "expires_in": 120})
	})
	dir := t.TempDir()
	atTerminal := func() *Client {
		manager := newManager(t, server, dir)
		manager.interactive = true
		return manager
	}
	expired := atTerminal()
	seedAdminCredentials(t, expired, oauth2.Token{AccessToken: "expired", RefreshToken: "revoked", TokenType: "Bearer", Expiry: time.Unix(1, 0)})
	done := make(chan error, 1)
	go func() { _, err := expired.controlToken(t.Context()); done <- err }()
	<-entered
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	logout := commandConfig{Auth: ClientConfig{Origin: server.URL, ConfigDir: dir, HTTPClient: installationtest.OriginClient(server)}}
	if err := executeCommand(ctx, []string{"logout"}, nil, nil, logout); err != nil {
		t.Fatalf("logout while another command signs in: %v", err)
	}
	close(release)
	if err := <-done; !errors.Is(err, errCredentialChanged) {
		t.Fatalf("sign-in after logout: %v, want %v", err, errCredentialChanged)
	}
	if token, err := atTerminal().controlToken(t.Context()); err != nil || token != "access-secret" {
		t.Fatalf("signed-out terminal command token=%q error=%v", token, err)
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
				installationtest.MountDeviceAuth(t, mux, func() string { return "https://identity.example/" }, nil, test.expires, 1, fail)
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
		`{` + installation + `"oauth":{"issuer":"ORIGIN/","cliClientID":"cli","adminAudience":"ADMIN","deviceFlowProfile":"unknown"}}`,
	} {
		t.Run(body, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				origin := "https://" + r.Host
				_, _ = io.WriteString(w, strings.NewReplacer("ORIGIN", origin, "ADMIN", installationtest.AdminAudience(origin)).Replace(body))
			}))
			t.Cleanup(server.Close)
			config := commandConfig{Auth: ClientConfig{ConfigDir: t.TempDir(), HTTPClient: installationtest.OriginClient(server)}}
			if err := executeCommand(t.Context(), []string{"login", server.URL}, nil, nil, config); !errors.Is(err, errMalformedResponse) {
				t.Fatalf("incompatible discovery: %v", err)
			}
		})
	}
}

func TestSavedCredentialsForAnotherIssuer(t *testing.T) {
	t.Parallel()
	manager := newManager(t, newAuthTestServer(t, nil), t.TempDir())
	seedAdminCredentials(t, manager, oauth2.Token{
		AccessToken: "access-token", RefreshToken: "refresh-token", TokenType: "Bearer", Expiry: time.Now().Add(time.Hour),
	})
	manager.state.OAuth.Issuer = "https://other.example/"
	if err := manager.writeState(manager.state); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.controlToken(t.Context()); !errors.Is(err, errEndpointChanged) {
		t.Fatalf("error = %v, want %v", err, errEndpointChanged)
	}
}

var clientCredentials = []string{"HOLON_CLIENT_ID=ci-deployer", "HOLON_CLIENT_SECRET=ci-secret"}

func machineTestConfig(t *testing.T, server *httptest.Server, env ...string) commandConfig {
	t.Helper()
	return commandConfig{Auth: ClientConfig{
		Env:        append([]string{"HOLON_ORIGIN=" + server.URL}, env...),
		HTTPClient: installationtest.OriginClient(server), ConfigDir: t.TempDir(),
	}}
}

func TestMachineCredentials(t *testing.T) {
	t.Parallel()
	for _, scenario := range []struct {
		name   string
		env    []string
		status int
		body   string
		want   error
	}{
		{"valid", clientCredentials, 200, `{"access_token":"machine-token","token_type":"Bearer"}`, nil},
		{"invalid secret", clientCredentials, 401, `{"error":"invalid_client","error_description":"ci-secret"}`, errOAuthFailed},
		{"invalid token", clientCredentials, 200, `{"access_token":"invalid token ci-secret","token_type":"Bearer"}`, errMalformedResponse},
		{"environment token", []string{"HOLON_TOKEN=machine-token"}, 0, "", nil},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			t.Parallel()
			server := installationtest.MachineAuthServer(t, func(w http.ResponseWriter, _ *http.Request) {
				if scenario.status == 0 {
					t.Error("token endpoint called")
					return
				}
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
			config := machineTestConfig(t, server, scenario.env...)
			var output strings.Builder
			err := executeCommand(t.Context(), []string{"config", "delete", "chat", "REGION", "--env", "prod"}, &output, &output, config)
			if !errors.Is(err, scenario.want) || strings.Contains(fmt.Sprint(err)+output.String(), "ci-secret") || strings.Contains(output.String(), "machine-token") {
				t.Fatalf("error = %v, output = %q", err, output.String())
			}
			if files, err := os.ReadDir(config.Auth.ConfigDir); err != nil || len(files) != 0 {
				t.Fatalf("machine authentication wrote local credentials: files=%v error=%v", files, err)
			}
		})
	}
	for _, invalid := range []struct {
		env  []string
		want string
	}{
		{[]string{"HOLON_CLIENT_ID=ci-deployer"}, "requires HOLON_CLIENT_ID and HOLON_CLIENT_SECRET together"},
		{[]string{"HOLON_CLIENT_ID=", "HOLON_CLIENT_SECRET="}, "requires HOLON_CLIENT_ID and HOLON_CLIENT_SECRET together"},
		{[]string{"HOLON_TOKEN="}, "HOLON_TOKEN must be a bearer token"},
		{append([]string{"HOLON_TOKEN=machine-token"}, clientCredentials...), "HOLON_TOKEN must be a bearer token"},
	} {
		config := commandConfig{Auth: ClientConfig{Env: invalid.env, Origin: "https://unused.example", ConfigDir: t.TempDir()}}
		if err := executeCommand(t.Context(), []string{"config", "delete", "chat", "REGION", "--env", "prod"}, nil, nil, config); err == nil ||
			!strings.Contains(err.Error(), invalid.want) {
			t.Fatalf("%v fell back: %v", invalid.env, err)
		}
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		installationtest.WriteJSON(w, protocol.Info{Installation: protocol.InstallationReference{Origin: "https://" + r.Host, UID: "test-installation"}})
	}))
	defer server.Close()
	if err := executeCommand(t.Context(), []string{"config", "delete", "chat", "REGION", "--env", "prod"}, nil, nil, machineTestConfig(t, server, clientCredentials...)); err == nil ||
		!strings.Contains(err.Error(), "requires an identity-enabled installation") {
		t.Fatalf("machine authentication on anonymous installation: %v", err)
	}
	if err := executeCommand(t.Context(), []string{"logout"}, nil, nil, machineTestConfig(t, server, clientCredentials...)); err == nil {
		t.Fatalf("machine logout = %v", err)
	}
}

func TestConcurrentAuthentication(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	var authorizations atomic.Int32
	server := newAuthTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.FormValue("grant_type") != "refresh_token" || r.FormValue("refresh_token") != "refresh-one" {
			t.Error("replayed a rotated refresh token")
		}
		if authorizations.Add(1) == 1 {
			close(entered)
		}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		installationtest.WriteJSON(w, map[string]any{"access_token": "shared-access", "token_type": "Bearer", "refresh_token": "refresh-two", "expires_in": 3600})
	})
	dir := t.TempDir()
	seedAdminCredentials(t, newManager(t, server, dir), oauth2.Token{
		AccessToken: "expired", RefreshToken: "refresh-one",
		TokenType: "Bearer", Expiry: time.Unix(1, 0),
	})
	tokens := make(chan error, 2)
	refresh := func(manager *Client) {
		token, err := manager.controlToken(ctx)
		if err == nil && token != "shared-access" {
			err = fmt.Errorf("unexpected token %q", token)
		}
		tokens <- err
	}
	go refresh(newManager(t, server, dir))
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	go refresh(newManager(t, server, dir))
	other := newAuthTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		installationtest.WriteJSON(w, map[string]any{"access_token": "other-access", "token_type": "Bearer", "refresh_token": "other-refresh", "expires_in": 3600})
	})
	if err := newManager(t, other, dir).loginInteractive(ctx); err != nil {
		t.Fatalf("another remote's authentication was blocked: %v", err)
	}
	close(release)
	for range 2 {
		if err := <-tokens; err != nil {
			t.Fatal(err)
		}
	}
	if authorizations.Load() != 1 {
		t.Fatalf("concurrent clients exchanged credentials %d times", authorizations.Load())
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
	manager, err := NewClient(t.Context(), machineTestConfig(t, server, clientCredentials...).Auth)
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
	if err != nil || other.AccessToken == token {
		t.Fatalf("audience cache collision: %v %v", requests, err)
	}
}

func TestSessionTokenComesFromTheSignIn(t *testing.T) {
	t.Parallel()
	var refreshes []string
	server := newAuthTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		refreshes = append(refreshes, r.FormValue("refresh_token")+"|"+r.FormValue("resource"))
		installationtest.WriteJSON(w, map[string]any{
			"token_type": "Bearer", "access_token": fmt.Sprintf("access-%d", len(refreshes)),
			"refresh_token": fmt.Sprintf("refresh-%d", len(refreshes)), "expires_in": 3600,
		})
	})
	dir := t.TempDir()
	manager := newManager(t, server, dir)
	seedAdminCredentials(t, manager, oauth2.Token{AccessToken: "admin", RefreshToken: "refresh-0", TokenType: "Bearer", Expiry: time.Now().Add(time.Hour)})
	session, err := protocol.SessionAudience(server.URL, protocol.ModeDevelopment, "s1")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if token, err := manager.resourceToken(t.Context(), session); err != nil || token.AccessToken != "access-1" {
			t.Fatalf("session token = %v, %v", token, err)
		}
	}
	other, _ := protocol.SessionAudience(server.URL, protocol.ModeDevelopment, "s2")
	if _, err := newManager(t, server, dir).resourceToken(t.Context(), other); err != nil {
		t.Fatal(err)
	}
	if want := []string{"refresh-0|" + session, "refresh-1|" + other}; !slices.Equal(refreshes, want) {
		t.Fatalf("refreshes = %v, want %v", refreshes, want)
	}
}

func seedAdminCredentials(t *testing.T, manager *Client, token oauth2.Token) {
	t.Helper()
	refresh := token.RefreshToken
	token.RefreshToken = ""
	manager.state = authState{
		OAuth: credentialTarget(manager.info.OAuth), Version: "test", Refresh: refresh,
		Access: map[string]*oauth2.Token{manager.info.OAuth.AdminAudience: &token},
	}
	if err := manager.withLock(t.Context(), func() error { return manager.writeState(manager.state) }); err != nil {
		t.Fatal(err)
	}
}
