package mcpregistry

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	"github.com/holon-sh/protocol"
)

func TestPackageTemplates(t *testing.T) {
	pkg := Package{
		RegistryType: "oci",
		Transport:    Transport{Type: "streamable-http", URL: "http://{HOST}:{--port}/mcp"},
		Environment: []json.RawMessage{
			json.RawMessage(`{"name":"HOST","default":"0.0.0.0"}`),
			json.RawMessage(`{"name":"API_URL","value":"https://{tenant}.example.com","isRequired":true,"variables":{"tenant":{}}}`),
		},
		RuntimeArguments: []json.RawMessage{
			json.RawMessage(`{"type":"named","name":"-e","value":"TOKEN={token}","variables":{"token":{"isSecret":true}}}`),
		},
		PackageArguments: []json.RawMessage{
			json.RawMessage(`{"type":"named","name":"--port","default":"9001"}`),
			json.RawMessage(`{"type":"positional","value":"https://ecs.{region}.api.aws/mcp","variables":{"region":{"default":"us-east-1"}}}`),
		},
	}
	if port, err := pkg.Port(); err != nil || port != 9001 {
		t.Fatalf("port = %d, %v", port, err)
	}
	app, err := pkg.App(nil, RunConfig{})
	env := app.Env
	if err != nil || !app.Imported || !env["tenant"].IsRequired || !env["token"].IsSecret || *env["region"].Default != "us-east-1" || *env["PORT"].Default != "9001" {
		t.Fatalf("variables = %v, %v", env, err)
	}
	if !reflect.DeepEqual(app.EnvTemplates, map[string]protocol.Template{"API_URL": "https://{tenant}.example.com", "TOKEN": "{token}"}) ||
		!slices.Equal(app.Args, []protocol.Template{"--port={PORT}", "https://ecs.{region}.api.aws/mcp"}) {
		t.Fatalf("templates = %v, args = %q", app.EnvTemplates, app.Args)
	}
	pkg.Transport.URL = "http://localhost:{PORT}/mcp"
	if _, err := pkg.Port(); err == nil {
		t.Fatal("port without a published value accepted")
	}
}
