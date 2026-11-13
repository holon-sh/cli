package mcptype

import (
	"encoding/json"
	"os/exec"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/holon-sh/cli/pkg/mcpcontract"
)

// The node module hands the runtime its app's identity and contracts, both for the ready-made apps and
// for createApps, which passes the caller's bindings and options through.
func TestNodeModulePassesItsIdentityAndContractsToTheRuntime(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	identity := mcpcontract.Identity{Name: "a'b\"\\\n\r\t\x00\x1f\x7fé😀  ", Version: "1"}
	requires := map[string]mcpcontract.Identity{"orders": {Name: "example.com/orders", Version: "1.0.0"}}
	var contracts map[string]*mcpcontract.Contract
	if err := json.Unmarshal([]byte(`{"orders": `+orderContract+`}`), &contracts); err != nil {
		t.Fatal(err)
	}
	files, err := Generate(t.Context(), GenerateRequest{Target: "node", Identity: identity, Requires: requires, Contracts: contracts})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Apps, Bound []any
		Error       bool
	}
	importModule(t, node, files, "holon-sh", "export const createApps = (...args) => args;\n", `import { apps, createApps, ToolCallError } from "./index.mjs";
console.log(JSON.stringify({ apps, bound: createApps({ orders: { endpoint: "mcp:" } }, { timeout: 1 }), error: new ToolCallError() instanceof Error }));
`, &got)
	want := []any{asJSON(t, map[string]any{"name": identity.Name, "version": identity.Version, "requires": requires}), asJSON(t, contracts)}
	if diff := cmp.Diff(want, got.Apps); diff != "" {
		t.Errorf("apps were created with (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(append(want, asJSON(t, map[string]any{"orders": map[string]any{"endpoint": "mcp:"}}), asJSON(t, map[string]any{"timeout": 1})), got.Bound); diff != "" {
		t.Errorf("createApps bound (-want +got):\n%s", diff)
	}
	if !got.Error {
		t.Error("the module does not re-export the runtime's ToolCallError")
	}
}

// The browser module connects the page to its own app at the endpoint Holon serves it on, passing
// connect's options through. Without an own app it has no connect and loads from a runtime without one.
func TestBrowserModuleConnectsToItsOwnApp(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	request := GenerateRequest{Target: "browser", Identity: mcpcontract.Identity{Name: "checkout", Version: "1"}}
	if err := json.Unmarshal([]byte(`{"my orders": `+orderContract+`}`), &request.Contracts); err != nil {
		t.Fatal(err)
	}
	files, err := Generate(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	var exported []string
	importModule(t, node, files, "holon-ui", "", `import * as module from "./index.mjs";
console.log(JSON.stringify(Object.keys(module)));
`, &exported)
	if want := []string{"ToolCallError", "createApps"}; !cmp.Equal(exported, want) {
		t.Errorf("without an own app the module exports %v, want %v", exported, want)
	}
	request.Own = "my orders"
	if files, err = Generate(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	var opened []any
	importModule(t, node, files, "holon-ui", "export const connect = (...args) => args;\n", `import { connect } from "./index.mjs";
console.log(JSON.stringify(connect({ tools: {} })));
`, &opened)
	app := map[string]any{"identity": map[string]any{"name": "checkout", "version": "1", "requires": map[string]any{}}, "contracts": request.Contracts, "own": "my orders", "endpoint": "/mcp/my%20orders"}
	if diff := cmp.Diff([]any{asJSON(t, app), asJSON(t, map[string]any{"tools": map[string]any{}})}, opened); diff != "" {
		t.Errorf("connect opened (-want +got):\n%s", diff)
	}
}

// importModule runs main beside the generated module, with a stand-in runtime package whose functions
// return what they were called with, and decodes what main prints into got.
func importModule(t *testing.T, node string, files map[string][]byte, pkg, runtime, main string, got any) {
	t.Helper()
	root := t.TempDir()
	writeFiles(t, root, map[string][]byte{
		TypeScriptModule:                        files[TypeScriptModule],
		"node_modules/" + pkg + "/package.json": []byte(`{"name":"` + pkg + `","type":"module","exports":{"./runtime":"./runtime.js"}}`),
		"node_modules/" + pkg + "/runtime.js":   []byte("export class ToolCallError extends Error {}\nexport const bindApps = (...args) => args;\n" + runtime),
		"main.mjs":                              []byte(main),
	})
	command := exec.CommandContext(t.Context(), node, "main.mjs")
	command.Dir = root
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("generated module failed to import: %v\n%s", err, output)
	}
	if err := json.Unmarshal(output, got); err != nil {
		t.Fatalf("%v: %s", err, output)
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
