package mcpregistry

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	"github.com/holon-sh/protocol"
)

func TestPackageTemplates(t *testing.T) {
	var pkg Package
	err := json.Unmarshal([]byte(`{"registryType":"oci","transport":{"type":"streamable-http","url":"http://{HOST}:{--port}/mcp"},
		"environmentVariables":[
			{"name":"HOST","default":"0.0.0.0"},
			{"name":"API_URL","value":"https://{tenant}.example.com","isRequired":true,"variables":{"tenant":{}}}
		],
		"runtimeArguments":[{"type":"named","name":"-e","value":"TOKEN={token}","variables":{"token":{"isSecret":true}}}],
		"packageArguments":[
			{"type":"named","name":"--port","default":"9001"},
			{"type":"positional","value":"https://ecs.{region}.api.aws/mcp","variables":{"region":{"default":"us-east-1"}}}
		]}`), &pkg)
	if err != nil {
		t.Fatal(err)
	}
	if port, err := pkg.Port(); err != nil || port != 9001 {
		t.Fatalf("port = %d, %v", port, err)
	}
	app, err := pkg.App(nil, RunConfig{})
	env := app.Env
	if err != nil || !env["tenant"].IsRequired || !env["token"].IsSecret || *env["region"].Default != "us-east-1" || *env["PORT"].Default != "9001" {
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
