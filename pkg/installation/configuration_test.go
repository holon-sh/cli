package installation

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/holon-sh/cli/pkg/installation/installationtest"
	"github.com/holon-sh/protocol"
)

func TestConfigurationCommands(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	sourceFiles(t, dir, map[string]string{manifestFileName: "invalid: project"})
	h := newRunHarness(t)
	var sent []string
	h.control.Handle([]string{protocol.ToolSetValue, protocol.ToolDeleteValue}, nil, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		sent = append(sent, strings.TrimSuffix(r.Host, h.baseHost)+" "+installationtest.ToolName(r)+" "+string(body))
		installationtest.WriteJSON(w, struct{}{})
	})
	h.control.Handle([]string{protocol.ToolGetEgress, protocol.ToolSetEgress}, nil, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		sent = append(sent, strings.TrimSuffix(r.Host, h.baseHost)+" "+installationtest.ToolName(r)+" "+string(body))
		var input protocol.SetEgressRequest
		_ = json.Unmarshal(body, &input)
		installationtest.WriteJSON(w, protocol.EgressResponse{AppEgressPolicy: input.Policy, Revision: "1"})
	})
	h.start(t, nil)
	var out bytes.Buffer
	for _, args := range [][]string{
		{"config", "set", "API_URL", "https://example.test"},
		{"config", "set", "REGION", ""},
		{"config", "delete", "API_URL", "--env", "prod"},
		{"secret", "delete", "API_URL", "--session", "s-one"},
		{"egress", "set", "alpha", "api.example.com:443"},
		{"egress", "set", "alpha"},
		{"egress", "delete", "*"},
	} {
		if err := executeCommand(t.Context(), args, &out, nil, h.run); err != nil {
			t.Fatal(err)
		}
	}
	if err := executeCommand(t.Context(), []string{"config", "set", "REGION", "sensitive\nline"}, nil, nil, h.run); err == nil ||
		strings.Contains(err.Error(), "sensitive") {
		t.Fatalf("invalid value diagnostics: %v", err)
	}
	want := []string{
		`dev. set_value {"kind":"config","variable":"API_URL","value":"https://example.test"}`,
		`dev. set_value {"kind":"config","variable":"REGION","value":""}`,
		`admin. delete_value {"kind":"config","variable":"API_URL"}`,
		`dev. delete_value {"session":"s-one","kind":"secret","variable":"API_URL"}`,
		`admin. get_egress {"app":"alpha"}`,
		`admin. set_egress {"app":"alpha","expectedRevision":"1","policy":{"allow":[{"host":"api.example.com","ports":[443]}]}}`,
		`admin. get_egress {"app":"alpha"}`,
		`admin. set_egress {"app":"alpha","expectedRevision":"1","policy":{"allow":[]}}`,
		`admin. get_egress {"app":"*"}`,
		`admin. set_egress {"app":"*","expectedRevision":"1","policy":{}}`,
	}
	if !slices.Equal(sent, want) {
		t.Fatalf("sent = %q, want %q", sent, want)
	}
	for _, scope := range []string{
		"config API_URL in developer-wide dev", "config API_URL in production", "secret API_URL in dev session s-one",
		"no egress", "none set",
	} {
		if !strings.Contains(out.String(), scope) {
			t.Fatalf("stdout = %q, want %q", out.String(), scope)
		}
	}
}
