package installation

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/holon-sh/cli/pkg/installation/installationtest"
	"github.com/holon-sh/protocol"
)

func assertSavedOrigin(t *testing.T, dir, want string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Origin string `json:"origin"`
	}
	if err := json.Unmarshal(data, &config); err != nil || config.Origin != want {
		t.Fatalf("saved client configuration = %s, %v; want origin %s", data, err, want)
	}
}

func TestCanonicalOrigin(t *testing.T) {
	t.Parallel()
	for raw, want := range map[string]string{
		"acme.example":          "https://acme.example",
		"localhost:30443":       "https://localhost:30443",
		"https://acme.example/": "https://acme.example",
		"":                      "",
		"acme.example/console":  "",
		"http://acme.example":   "",
	} {
		got, err := canonicalOrigin(raw)
		if want == "" && (err == nil || err.Error() != fmt.Sprintf("origin %q is invalid: expected HOST[:PORT] or https://HOST[:PORT]", raw)) ||
			want != "" && (err != nil || got != want) {
			t.Errorf("canonicalOrigin(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
}

func TestClientOriginPrecedence(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, environment string
		flag              bool
	}{
		{name: "flag bare host", flag: true, environment: "invalid"},
		{name: "environment", environment: "SELECTED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mux := http.NewServeMux()
			server := httptest.NewTLSServer(mux)
			t.Cleanup(server.Close)
			mux.HandleFunc(protocol.APIPathInfo, func(w http.ResponseWriter, _ *http.Request) {
				installationtest.WriteJSON(w, protocol.Info{Installation: protocol.InstallationReference{Origin: server.URL, UID: "installation"}})
			})
			installationtest.NewControlFixtures(t, mux).Handle([]string{protocol.ToolDeleteValue}, nil, func(w http.ResponseWriter, _ *http.Request) {
				installationtest.WriteJSON(w, struct{}{})
			})
			config := commandConfig{
				Env:  []string{"HOLON_ORIGIN=" + strings.ReplaceAll(tc.environment, "SELECTED", server.URL)},
				Auth: ClientConfig{ConfigDir: t.TempDir(), HTTPClient: installationtest.OriginClient(server)},
			}
			sourceFiles(t, config.Auth.ConfigDir, map[string]string{"config.json": "invalid"})
			args := []string{"config", "delete", "REGION", "--env", "prod"}
			if tc.flag {
				args = append(args, "--origin", strings.TrimPrefix(server.URL, "https://"))
			}
			if err := executeCommand(t.Context(), args, nil, nil, config); err != nil {
				t.Fatal(err)
			}
			if data, err := os.ReadFile(filepath.Join(config.Auth.ConfigDir, "config.json")); err != nil || string(data) != "invalid" {
				t.Fatalf("ordinary command changed selection: %s, %v", data, err)
			}
		})
	}
}

func TestInstallationCommandErrorsBeforeNetwork(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, saved, want string
		args              []string
	}{
		{
			name: "empty flag", args: []string{"config", "delete", "REGION", "--env", "prod", "--origin="},
			saved: `{"origin":"https://saved.example"}`, want: `origin "" is invalid`,
		},
		{name: "invalid login target", args: []string{"login", "acme.example/console"}, want: `origin "acme.example/console" is invalid`},
		{name: "invalid saved selection", saved: `{"origin":"acme.example/console"}`, want: `origin "acme.example/console" is invalid`},
		{name: "malformed saved configuration", saved: `{`, want: "read Holon configuration"},
		{name: "no selection", want: "no Holon installation selected"},
		{name: "empty saved selection", saved: `{"origin":""}`, want: "no Holon installation selected"},
		{name: "conflicting login targets", args: []string{"login", "https://one.example", "--origin", "https://two.example"}, want: "cannot be combined"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			config := commandConfig{
				Auth: ClientConfig{
					ConfigDir: t.TempDir(),
					HTTPClient: installationtest.HTTPClient(func(*http.Request) (*http.Response, error) {
						t.Error("invalid client configuration contacted a server")
						return nil, errors.New("unexpected request")
					}),
				},
			}
			if tc.saved != "" {
				sourceFiles(t, config.Auth.ConfigDir, map[string]string{"config.json": tc.saved})
			}
			args := tc.args
			if args == nil {
				args = []string{"config", "delete", "REGION", "--env", "prod"}
			}
			if err := executeCommand(t.Context(), args, nil, nil, config); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("invalid selection: %v; want %s", err, tc.want)
			}
		})
	}
}
