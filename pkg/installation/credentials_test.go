package installation

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"golang.org/x/oauth2"

	"github.com/holon-sh/cli/pkg/installation/installationtest"
	"github.com/holon-sh/protocol"
)

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

func TestDevelopmentAuthenticationBeforeBuild(t *testing.T) {
	t.Parallel()
	built := errors.New("build reached")
	for _, test := range []struct {
		name string
		want error
	}{
		{"authorized", built}, {"denied", errDeviceDenied},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var authorized atomic.Bool
			server := newAuthTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
				if test.name == "denied" {
					w.Header().Set(protocol.HeaderContentType, protocol.MIMEApplicationJSON)
					w.WriteHeader(http.StatusBadRequest)
					installationtest.WriteJSON(w, map[string]string{"error": "access_denied"})
					return
				}
				authorized.Store(true)
				installationtest.WriteJSON(w, map[string]any{"access_token": "access-token", "refresh_token": "refresh-token", "token_type": "Bearer", "expires_in": 120})
			})
			root := t.TempDir()
			sourceFiles(t, root, map[string]string{
				"package.json": sourcePackage, "server.js": "export default {}",
				manifestFileName: "apps:\n- name: alpha\n  path: .\n  access: {server: {}}\n",
			})
			config := commandConfig{
				Auth: ClientConfig{Origin: server.URL, ConfigDir: t.TempDir(), HTTPClient: installationtest.OriginClient(server)},
				builder: func(context.Context, buildRequest, io.Writer) (string, error) {
					if !authorized.Load() {
						t.Error("build started before development authorization")
					}
					return "", built
				},
			}
			args := []string{"dev", "--project", filepath.Join(root, manifestFileName)}
			if err := executeCommand(t.Context(), args, nil, nil, config); !errors.Is(err, test.want) {
				t.Fatalf("dev returned %v, want %v", err, test.want)
			}
		})
	}
}

func TestAppTokenSignsInOnlyToTheInstallationsApps(t *testing.T) {
	t.Parallel()
	server := newAuthTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		installationtest.WriteJSON(w, map[string]any{
			"access_token": "access-" + r.FormValue("resource"), "refresh_token": "refresh", "token_type": "Bearer", "expires_in": 3600,
		})
	})
	var output bytes.Buffer
	config := ClientConfig{Origin: server.URL, ConfigDir: t.TempDir(), HTTPClient: server.Client(), Output: &output}
	if token, err := config.AppToken(t.Context(), "https://example.com"); token != "" || err != nil {
		t.Fatalf("another origin: token=%q error=%v", token, err)
	}
	session, err := protocol.SessionAudience(server.URL, protocol.ModeDevelopment, "calm-fox-42")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if token, err := config.AppToken(t.Context(), session); token != "access-"+session || err != nil {
			t.Fatalf("session origin: token=%q error=%v", token, err)
		}
	}
	if prompts := strings.Count(output.String(), "Code: "); prompts != 2 {
		t.Fatalf("signed in %d times, want once for the installation and once for the session:\n%s", prompts, &output)
	}
}
