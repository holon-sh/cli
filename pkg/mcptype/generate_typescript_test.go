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
	// A stand-in runtime returns what each of its functions was called with.
	root := t.TempDir()
	writeFiles(t, root, map[string][]byte{
		TypeScriptModule:                     files[TypeScriptModule],
		"node_modules/holon-sh/package.json": []byte(`{"name":"holon-sh","type":"module","exports":{"./runtime":"./runtime.js"}}`),
		"node_modules/holon-sh/runtime.js": []byte(`export class ToolCallError extends Error {}
export const createApps = (...args) => args, bindApps = (...args) => args;
`),
		"main.mjs": []byte(`import { apps, createApps, ToolCallError } from "./index.mjs";
console.log(JSON.stringify({ apps, bound: createApps({ orders: { endpoint: "mcp:" } }, { timeout: 1 }), error: new ToolCallError() instanceof Error }));
`),
	})
	command := exec.CommandContext(t.Context(), node, "main.mjs")
	command.Dir = root
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("generated module failed to import: %v\n%s", err, output)
	}
	var got struct {
		Apps, Bound []any
		Error       bool
	}
	if err := json.Unmarshal(output, &got); err != nil {
		t.Fatalf("%v: %s", err, output)
	}
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
