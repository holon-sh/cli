package mcptype

import (
	"encoding/json"
	"os/exec"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/holon-sh/cli/pkg/mcpcontract"
)

func TestModulesPassTheirIdentityAndContractsToTheRuntime(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	identity := mcpcontract.Identity{Name: "a'b\"\\\n\r\t\x00\x1f\x7fé😀  ", Version: "1"}
	requires := map[string]mcpcontract.Identity{"orders": {Name: "example.com/orders", Version: "1.0.0"}}
	request := GenerateRequest{Identity: identity, Requires: requires}
	if err := json.Unmarshal([]byte(`{"orders": `+orderContract+`}`), &request.Contracts); err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	for _, module := range []struct{ name, target, own string }{{"plain", "browser", ""}, {"page", "browser", "orders"}, {"server", "node", ""}} {
		request.Target, request.Own, request.Endpoint = module.target, module.own, "/mcp/shop"
		generated, err := Generate(t.Context(), request)
		if err != nil {
			t.Fatal(err)
		}
		files[module.name+".mjs"] = generated[TypeScriptModule]
	}
	// A stand-in runtime returns what each of its functions was called with.
	runtime := []byte("export class ToolCallError extends Error {}\nexport const createApps = (...args) => args, bindApps = createApps, connect = createApps;\n")
	files["main.mjs"] = []byte(`import { apps, createApps, ToolCallError } from "./server.mjs";
import { connect } from "./page.mjs";
import * as plain from "./plain.mjs";
console.log(JSON.stringify({ apps, bound: createApps({ orders: { endpoint: "mcp:" } }, { timeout: 1 }), opened: connect({ timeout: 1 }), plainConnects: "connect" in plain }));
`)
	for _, pkg := range []string{"holon-sh", "holon-ui"} {
		files["node_modules/"+pkg+"/package.json"] = []byte(`{"name":"` + pkg + `","type":"module","exports":{"./runtime":"./runtime.js"}}`)
		files["node_modules/"+pkg+"/runtime.js"] = runtime
	}
	root := t.TempDir()
	writeFiles(t, root, files)
	command := exec.CommandContext(t.Context(), node, "main.mjs")
	command.Dir = root
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("generated modules failed to import: %v\n%s", err, output)
	}
	var got struct {
		Apps, Bound, Opened []any
		PlainConnects       bool
	}
	if err := json.Unmarshal(output, &got); err != nil {
		t.Fatalf("%v: %s", err, output)
	}
	want := []any{asJSON(t, map[string]any{"name": identity.Name, "version": identity.Version, "requires": requires}), asJSON(t, request.Contracts)}
	if diff := cmp.Diff(want, got.Apps); diff != "" {
		t.Errorf("apps were created with (-want +got):\n%s", diff)
	}
	options := asJSON(t, map[string]any{"timeout": 1})
	if diff := cmp.Diff(append(want, asJSON(t, map[string]any{"orders": map[string]any{"endpoint": "mcp:"}}), options), got.Bound); diff != "" {
		t.Errorf("createApps bound (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]any{asJSON(t, map[string]any{"identity": want[0], "contracts": want[1], "own": "orders", "endpoint": "/mcp/shop"}), options}, got.Opened); diff != "" {
		t.Errorf("connect opened (-want +got):\n%s", diff)
	}
	if got.PlainConnects {
		t.Error("a page without its own app has connect")
	}
}

// asJSON is value as JavaScript's JSON.stringify sends it back.
func asJSON(t *testing.T, value any) any {
	t.Helper()
	data, err := json.Marshal(value)
	if err == nil {
		err = json.Unmarshal(data, &value)
	}
	if err != nil {
		t.Fatal(err)
	}
	return value
}
