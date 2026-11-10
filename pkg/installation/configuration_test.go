package installation

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

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
		{"config", "delete", "API_URL", "--env", "prod"},
		{"egress", "set", "alpha", "api.example.com:443"},
		{"egress", "set", "alpha"},
		{"egress", "delete", "*"},
	} {
		if err := executeCommand(t.Context(), args, &out, nil, h.run); err != nil {
			t.Fatal(err)
		}
	}
	if err := executeCommandWithInput(t.Context(), []string{"secret", "set", "API_KEY", "--session", "s-one"}, strings.NewReader("piped-secret\n"), &out, nil, h.run); err != nil {
		t.Fatal(err)
	}
	if err := executeCommandWithInput(t.Context(), []string{"secret", "set", "API_KEY"}, strings.NewReader("\n"), nil, nil, h.run); err == nil ||
		!strings.Contains(err.Error(), "secret value is empty") {
		t.Fatalf("empty secret: %v", err)
	}
	if err := executeCommandWithInput(t.Context(), []string{"secret", "set", "API_KEY"}, strings.NewReader("sensitive\n\n"), nil, nil, h.run); err == nil ||
		strings.Contains(err.Error(), "sensitive") {
		t.Fatalf("invalid value diagnostics: %v", err)
	}
	want := []string{
		`dev. set_value {"kind":"config","variable":"API_URL","value":"https://example.test"}`,
		`admin. delete_value {"kind":"config","variable":"API_URL"}`,
		`admin. get_egress {"app":"alpha"}`,
		`admin. set_egress {"app":"alpha","expectedRevision":"1","policy":{"allow":[{"host":"api.example.com","ports":[443]}]}}`,
		`admin. get_egress {"app":"alpha"}`,
		`admin. set_egress {"app":"alpha","expectedRevision":"1","policy":{"allow":[]}}`,
		`admin. get_egress {"app":"*"}`,
		`admin. set_egress {"app":"*","expectedRevision":"1","policy":{}}`,
		`dev. set_value {"session":"s-one","kind":"secret","variable":"API_KEY","value":"piped-secret"}`,
	}
	if !slices.Equal(sent, want) {
		t.Fatalf("sent = %q, want %q", sent, want)
	}
	for _, scope := range []string{
		"config API_URL in developer-wide dev", "config API_URL in production", "secret API_KEY in dev session s-one",
		"no egress", "none set",
	} {
		if !strings.Contains(out.String(), scope) {
			t.Fatalf("stdout = %q, want %q", out.String(), scope)
		}
	}
}

func TestSourceAppsReceiveSetValues(t *testing.T) {
	h := newRunHarness(t)
	var planes []string
	h.control.Handle([]string{protocol.ToolListValues}, nil, func(w http.ResponseWriter, r *http.Request) {
		planes = append(planes, strings.TrimSuffix(r.Host, h.baseHost))
		installationtest.WriteJSON(w, protocol.ConfigValues{Values: []protocol.ConfigValue{
			{Kind: "config", Variable: "REGION"}, {Kind: "secret", Variable: "API_KEY"}, {Kind: "config", Variable: "DECLARED"},
			{Kind: "config", Variable: "PORT"}, {Kind: "config", Variable: "HOLON_OVERLAY_FILE"}, {Kind: "secret", Variable: "ORDERS_MCP_URL"},
			{Kind: "config", Variable: "CLIENT_SECRET"}, {Session: "resumed", Kind: "secret", Variable: "SESSION_KEY"},
		}})
	})
	h.start(t, nil)
	manager, err := NewClient(t.Context(), h.run.Auth)
	if err != nil {
		t.Fatal(err)
	}
	manifest := manifestSpec{Apps: []manifestApp{{Name: "app", Path: "app"}, {Name: "remote"}}}
	for _, name := range []string{"", protocol.ProductionName} {
		apps := []protocol.App{
			{Name: "app", Requires: []string{"orders"}, Env: map[string]protocol.Input{"DECLARED": {Value: new("fixed")}}},
			{Name: "remote", OAuth: &protocol.OAuthClient{ClientID: "id", ClientSecret: "CLIENT_SECRET"}},
		}
		if err := declareSetValues(t.Context(), manager, name, manifest, apps); err != nil {
			t.Fatal(err)
		}
		want := map[string]protocol.Input{
			"REGION": {}, "API_KEY": {IsSecret: true}, "SESSION_KEY": {IsSecret: true}, "DECLARED": {Value: new("fixed")},
		}
		if diff := cmp.Diff(want, apps[0].Env); diff != "" || apps[1].Env != nil {
			t.Fatalf("%q environment (-want +got):\n%s\nremote: %v", name, diff, apps[1].Env)
		}
	}
	if want := []string{"dev.", "admin."}; !slices.Equal(planes, want) {
		t.Fatalf("planes = %v, want %v", planes, want)
	}
}
