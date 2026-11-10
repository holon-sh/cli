package installation

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

	"github.com/holon-sh/cli/pkg/installation/installationtest"
	"github.com/holon-sh/protocol"
)

func TestCredentialAudienceIsolation(t *testing.T) {
	t.Parallel()
	var refreshes atomic.Int32
	server := newAuthTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.FormValue("grant_type") != "refresh_token" {
			t.Error("saved audience required another device authorization")
		}
		refresh := r.FormValue("refresh_token")
		refreshes.Add(1)
		installationtest.WriteJSON(w, map[string]any{
			"access_token":  strings.TrimSuffix(refresh, "-refresh") + "-access-" + r.FormValue("resource"),
			"refresh_token": refresh + "-rotated", "token_type": "Bearer", "expires_in": 3600,
		})
	})
	dir := t.TempDir()
	manager := newManager(t, server, dir)
	manager.state.OAuth, manager.state.Version = targetForOAuth(manager.info.OAuth), "test"
	session, err := protocol.SessionAudience(server.URL, protocol.ModeDevelopment, "calm-fox-42")
	if err != nil {
		t.Fatal(err)
	}
	audiences := map[string]string{"control": installationtest.AdminAudience(server.URL), "session": session}
	for name, audience := range audiences {
		manager.state.Tokens[audience] = &oauth2.Token{
			AccessToken: "expired", RefreshToken: name + "-refresh", TokenType: "Bearer", Expiry: time.Unix(1, 0),
		}
	}
	if err := manager.withLock(t.Context(), func() error { return manager.writeState(manager.state) }); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		for name, audience := range audiences {
			manager := newManager(t, server, dir)
			token, err := manager.resourceToken(t.Context(), audience)
			if err != nil || token.AccessToken != name+"-access-"+audience {
				t.Fatalf("audience %s: token=%v error=%v", audience, token, err)
			}
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
			manager := newManager(t, server, t.TempDir())
			seedAdminCredentials(t, manager, oauth2.Token{
				AccessToken: "private-access", RefreshToken: "private-refresh",
				TokenType: "Bearer",
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
			if _, err := manager.controlToken(t.Context()); !errors.Is(err, errCredentialUnavailable) {
				t.Fatalf("unsafe credentials: %v", err)
			}
		})
	}
}

func TestSignedOutCommandsFailWithoutSigningIn(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	sourceFiles(t, root, map[string]string{
		"package.json": sourcePackage, "server.js": "export default {}",
		manifestFileName: "apps:\n- name: alpha\n  path: .\n  access: {server: {}}\n",
	})
	for _, args := range [][]string{{"app", "list"}, {"dev", "--project", filepath.Join(root, manifestFileName)}} {
		t.Run(args[0], func(t *testing.T) {
			t.Parallel()
			mux := http.NewServeMux()
			var server *httptest.Server
			installationtest.MountDeviceAuth(t, mux, func() string { return server.URL + "/" }, protocol.AdminPlane, func(r *http.Request) {
				if r.URL.Path != protocol.APIPathInfo {
					t.Errorf("signed-out command contacted %s", r.URL.Path)
				}
			}, 120, 1, nil)
			server = httptest.NewTLSServer(mux)
			t.Cleanup(server.Close)
			config := commandConfig{
				Auth: ClientConfig{Origin: server.URL, ConfigDir: t.TempDir(), HTTPClient: installationtest.OriginClient(server)},
				builder: func(context.Context, buildRequest, io.Writer) (string, error) {
					t.Error("signed-out command started a build")
					return "", nil
				},
			}
			err := executeCommand(t.Context(), args, nil, nil, config)
			if want := "not signed in; run holon login " + server.URL; err == nil || err.Error() != want {
				t.Fatalf("error = %v, want %q", err, want)
			}
		})
	}
}
