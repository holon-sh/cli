package cloud

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/holon-sh/cli/pkg/protocol"
	"github.com/holon-sh/cli/pkg/cloud/cloudtest"
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

func TestClientOriginPrecedence(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, saved, environment, flag string
	}{
		{name: "override without saved configuration", flag: "SELECTED"},
		{name: "flag", flag: "SELECTED", environment: "invalid", saved: "invalid"},
		{name: "environment", environment: "SELECTED", saved: "invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mux := http.NewServeMux()
			server := httptest.NewTLSServer(mux)
			t.Cleanup(server.Close)
			host := strings.TrimPrefix(server.URL, "https://")
			mux.HandleFunc(protocol.APIPathInfo, func(w http.ResponseWriter, r *http.Request) {
				cloudtest.AssertOrigin(t, r, host)
				cloudtest.WriteJSON(w, protocol.Info{Installation: protocol.InstallationReference{Origin: server.URL, UID: "installation"}})
			})
			cloudtest.NewControlFixtures(t, mux).Handle([]string{protocol.ToolDeleteValue}, nil, func(w http.ResponseWriter, r *http.Request) {
				cloudtest.AssertOrigin(t, r, "admin."+host)
				cloudtest.WriteJSON(w, struct{}{})
			})
			config := commandConfig{Auth: ClientConfig{
				ConfigDir: t.TempDir(), HTTPClient: cloudtest.OriginClient(server),
			}}
			saved := strings.ReplaceAll(tc.saved, "SELECTED", server.URL)
			if saved != "" {
				sourceFiles(t, config.Auth.ConfigDir, map[string]string{"config.json": saved})
			}
			config.Env = []string{"HOLON_ORIGIN=" + strings.ReplaceAll(tc.environment, "SELECTED", server.URL)}
			args := []string{"config", "delete", "REGION", "--env", "prod"}
			if tc.flag != "" {
				args = append(args, "--origin", strings.ReplaceAll(tc.flag, "SELECTED", server.URL))
			}
			if err := executeCommand(t.Context(), args, nil, nil, config); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(config.Auth.ConfigDir, "config.json"))
			if saved == "" {
				if !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("ordinary command saved a selection: %s, %v", data, err)
				}
			} else if err != nil || string(data) != saved {
				t.Fatalf("ordinary command changed selection: %s, %v", data, err)
			}
		})
	}
}

func TestClientOriginErrorsBeforeNetwork(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, saved, environment, want string
		args                           []string
	}{
		{
			name: "invalid flag", args: []string{"config", "delete", "REGION", "--env", "prod", "--origin", "http://insecure.example"},
			saved: `{"origin":"https://saved.example"}`, want: "public origin is invalid",
		},
		{
			name: "empty flag", args: []string{"config", "delete", "REGION", "--env", "prod", "--origin="},
			saved: `{"origin":"https://saved.example"}`, want: "public origin is invalid",
		},
		{name: "invalid environment", environment: "invalid", saved: `{"origin":"https://saved.example"}`, want: "public origin is invalid"},
		{name: "invalid saved origin", saved: `{"origin":"http://insecure.example"}`, want: "public origin is invalid"},
		{name: "malformed saved configuration", saved: `{`, want: "read Holon configuration"},
		{name: "conflicting login targets", args: []string{"login", "https://one.example", "--origin", "https://two.example"}, want: "cannot be combined"},
		{name: "empty login target", args: []string{"login", ""}, saved: `{"origin":"https://saved.example"}`, want: "public origin is invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			config := commandConfig{
				Env: []string{"HOLON_ORIGIN=" + tc.environment},
				Auth: ClientConfig{
					ConfigDir: t.TempDir(),
					HTTPClient: cloudtest.HTTPClient(func(*http.Request) (*http.Response, error) {
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
