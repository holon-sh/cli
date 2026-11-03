package cloud

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/holon-sh/cli/pkg/protocol"
	"github.com/holon-sh/cli/pkg/cloud/cloudtest"
)

func TestConfigurationSetSendsValuesAsGiven(t *testing.T) {
	h := newRunHarness(t)
	sent := map[string]string{}
	h.control.Handle([]string{protocol.ToolSetValue}, nil, func(w http.ResponseWriter, r *http.Request) {
		cloudtest.AssertOrigin(t, r, "dev."+h.baseHost)
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		value, present := body["value"]
		if !present {
			t.Errorf("value lost: %#v", body)
		}
		sent[body["variable"]] = value
		cloudtest.WriteJSON(w, struct{}{})
	})
	h.start(t)
	var out bytes.Buffer
	for _, args := range [][]string{{"config", "set", "API_URL", "https://example.test"}, {"config", "set", "REGION", ""}} {
		if err := executeCommand(t.Context(), args, &out, &bytes.Buffer{}, h.run); err != nil {
			t.Fatal(err)
		}
	}
	for _, invalid := range []string{"sensitive\x00", "sensitive\nline", "sensitive\rline"} {
		if err := executeCommand(t.Context(), []string{"config", "set", "REGION", invalid}, &out, &bytes.Buffer{}, h.run); err == nil ||
			strings.Contains(err.Error(), "sensitive") {
			t.Fatalf("invalid value diagnostics: %v", err)
		}
	}
	if len(sent) != 2 || sent["API_URL"] != "https://example.test" || sent["REGION"] != "" {
		t.Fatalf("sent = %#v", sent)
	}
	if !strings.Contains(out.String(), "Stored config API_URL in developer-wide dev") {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestConfigurationScopes(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	sourceFiles(t, dir, map[string]string{manifestFileName: "invalid: project"})
	for _, test := range []struct {
		name        string
		args        []string
		environment string
		session     string
	}{
		{
			name:        "production",
			args:        []string{"config", "delete", "API_URL", "--env", "prod"},
			environment: "prod",
		},
		{
			name:        "session",
			args:        []string{"secret", "delete", "API_URL", "--session", "s-one"},
			environment: "dev",
			session:     "s-one",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newRunHarness(t)
			h.control.Handle([]string{protocol.ToolDeleteValue}, map[string]string{"variable": "API_URL"}, func(w http.ResponseWriter, r *http.Request) {
				if cloudtest.ToolName(r) != protocol.ToolDeleteValue {
					t.Errorf("method = %s", r.Method)
				}
				plane := "dev."
				if test.environment == "prod" {
					plane = "admin."
				}
				cloudtest.AssertOrigin(t, r, plane+h.baseHost)
				if _, found := r.URL.Query()["env"]; found {
					t.Errorf("wire environment was sent: %s", r.URL.RawQuery)
				}
				input := protocol.VariableRequest{}
				_ = json.NewDecoder(r.Body).Decode(&input)
				if got := input.Session; got != test.session {
					t.Errorf("session = %q, want %q", got, test.session)
				}
				cloudtest.WriteJSON(w, struct{}{})
			})
			h.start(t)
			if err := executeCommand(t.Context(), test.args, &bytes.Buffer{}, &bytes.Buffer{}, h.run); err != nil {
				t.Fatal(err)
			}
		})
	}
}
