package cloud

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/oauth2"
	testingclock "k8s.io/utils/clock/testing"

	"github.com/holon-sh/cli/pkg/cloud/cloudtest"
	"github.com/holon-sh/protocol"
)

func TestCredentialAudienceIsolation(t *testing.T) {
	t.Parallel()
	clock := testingclock.NewFakePassiveClock(time.Unix(100, 0))
	var refreshes atomic.Int32
	server := newAuthTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.FormValue("grant_type") != "refresh_token" {
			t.Error("saved audience required another device authorization")
		}
		refresh := r.FormValue("refresh_token")
		refreshes.Add(1)
		cloudtest.WriteJSON(w, map[string]any{
			"access_token":  strings.TrimSuffix(refresh, "-refresh") + "-access",
			"refresh_token": refresh + "-rotated", "token_type": "Bearer", "expires_in": 3600,
		})
	})
	dir := t.TempDir()
	manager := newManager(t, server, clock, dir)
	manager.state.OAuth, manager.state.Version = targetForOAuth(manager.info.OAuth), "test"
	for plane, refresh := range map[protocol.Plane]string{protocol.AdminPlane: "admin-refresh", protocol.DevPlane: "dev-refresh"} {
		manager.state.Tokens[tokenKey(manager.info.OAuth.Issuer, manager.audience(plane), protocol.ControlScopes(plane))] = &oauth2.Token{
			AccessToken: "expired", RefreshToken: refresh, TokenType: "Bearer", ExpiresIn: 3600, Expiry: time.Unix(1, 0),
		}
	}
	if err := manager.withLock(t.Context(), func() error { return manager.writeState(manager.state) }); err != nil {
		t.Fatal(err)
	}
	for _, plane := range []protocol.Plane{protocol.DevPlane, protocol.AdminPlane, protocol.DevPlane, protocol.AdminPlane} {
		manager := newManager(t, server, clock, dir)
		token, err := manager.controlToken(t.Context(), plane)
		want := "dev-access"
		if plane == protocol.AdminPlane {
			want = "admin-access"
		}
		if err != nil || token != want {
			t.Fatalf("audience %s: token=%q error=%v", plane, token, err)
		}
	}
	if refreshes.Load() != 2 {
		t.Fatalf("audiences did not reuse their own refreshed tokens: %d", refreshes.Load())
	}
}

func TestPrivateCredentialFiles(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"file permissions", "directory permissions", "symlink", "oversized"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			server := newAuthTestServer(t, func(http.ResponseWriter, *http.Request) { t.Error("unsafe credentials triggered authorization") })
			manager := newManager(t, server, testingclock.NewFakePassiveClock(time.Unix(100, 0)), t.TempDir())
			seedAdminCredentials(t, manager, oauth2.Token{
				AccessToken: "private-access", RefreshToken: "private-refresh",
				TokenType: "Bearer", ExpiresIn: 3600, Expiry: time.Unix(3700, 0),
			})
			path := manager.statePath
			switch kind {
			case "file permissions":
				if err := os.Chmod(path, 0o644); err != nil {
					t.Fatal(err)
				}
			case "directory permissions":
				if err := os.Chmod(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				target := filepath.Join(t.TempDir(), "outside")
				if err := os.Rename(path, target); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			case "oversized":
				if err := os.WriteFile(path, []byte(strings.Repeat("x", maxCredentialBytes+1)), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := manager.controlToken(t.Context(), protocol.AdminPlane); !errors.Is(err, errCredentialUnavailable) {
				t.Fatalf("unsafe credentials: %v", err)
			}
		})
	}
}

func TestDevelopmentAuthenticationBeforeBuild(t *testing.T) {
	t.Parallel()
	built := errors.New("build reached")
	for _, test := range []struct {
		name string
		want error
	}{
		{"authorized", built}, {"denied", errDeviceDenied}, {"cancelled", context.Canceled},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var authorized atomic.Bool
			var server *httptest.Server
			mux := http.NewServeMux()
			cloudtest.MountDeviceAuth(t, mux, func() string { return server.URL + "/" }, protocol.DevPlane, nil, 120, 1,
				func(w http.ResponseWriter, _ *http.Request) {
					if test.name == "denied" {
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(http.StatusBadRequest)
						cloudtest.WriteJSON(w, map[string]string{"error": "access_denied"})
						return
					}
					authorized.Store(true)
					cloudtest.WriteJSON(w, map[string]any{"access_token": "dev-access", "refresh_token": "dev-refresh", "token_type": "Bearer", "expires_in": 120})
				})
			server = httptest.NewTLSServer(mux)
			t.Cleanup(server.Close)
			root := t.TempDir()
			sourceFiles(t, root, map[string]string{
				"package.json": sourcePackage, "server.js": "export default {}",
				manifestFileName: "apps:\n- name: alpha\n  path: .\n  access: {server: {}}\n",
			})
			config := commandConfig{
				Auth: ClientConfig{Origin: server.URL, ConfigDir: t.TempDir(), HTTPClient: cloudtest.OriginClient(server)},
				builder: func(context.Context, buildRequest, io.Writer) (buildResult, error) {
					if !authorized.Load() || test.name != "authorized" {
						t.Error("build started before development authorization")
					}
					return buildResult{}, built
				},
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			output := &cloudtest.ObservedOutput{OnWrite: func(text string) {
				if test.name == "cancelled" && strings.Contains(text, "Code:") {
					cancel()
				}
			}}
			args := []string{"dev", "--project", filepath.Join(root, manifestFileName)}
			if err := executeCommand(ctx, args, nil, output, config); !errors.Is(err, test.want) {
				t.Fatalf("dev returned %v, want %v", err, test.want)
			}
		})
	}
}
