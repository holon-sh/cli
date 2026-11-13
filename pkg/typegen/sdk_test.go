package typegen

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/holon-sh/cli/pkg/mcpcontract"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const editsSchema = `{"type":"object","required":["edits"],"properties":{"edits":{"type":"array","items":{
	"oneOf":[{"$ref":"#/$defs/Updated"},{"$ref":"#/$defs/Removed"}],
	"discriminator":{"propertyName":"op","mapping":{"cell.updated":"#/$defs/Updated","cell.removed":"#/$defs/Removed"}}}}},
"$defs":{
	"Updated":{"type":"object","required":["id","code"],"properties":{"op":{"const":"cell.updated","default":"cell.updated","type":"string"},"id":{"type":"string"},"code":{"type":"string"}}},
	"Removed":{"type":"object","required":["id"],"properties":{"op":{"const":"cell.removed","default":"cell.removed","type":"string"},"id":{"type":"string"}}}}}`

const consumerTSConfig = `{"compilerOptions":{"strict":true,"noEmit":true,"skipLibCheck":%t,"target":"ES2023","module":"NodeNext"},"include":["src"]}`

// Generated clients must agree with the installed SDK's declarations and runtime, including
// through its package exports. These dependencies can be replaced with candidate distributions.
func TestGeneratedSDKConsumers(t *testing.T) {
	orders := ordersMetadata()
	orders.Schema.Events = []mcpcontract.Event{{Name: "cells.edited",
		Input:   json.RawMessage(`{"type":"object","properties":{"notebook":{"type":"string"}},"required":["notebook"],"additionalProperties":false}`),
		Payload: json.RawMessage(editsSchema)}}
	server := mcp.NewServer(&mcp.Implementation{Name: "orders", Version: "1"}, nil)
	server.AddTool(&mcp.Tool{Name: "lookup", InputSchema: lookupTool.Input}, func(_ context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var arguments struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(request.Params.Arguments, &arguments); err != nil {
			return nil, err
		}
		var total any = 1
		if arguments.ID == "bad" {
			total = "changed server output"
		}
		return &mcp.CallToolResult{Content: []mcp.Content{}, StructuredContent: map[string]any{"total": total}}, nil
	})
	endpoint := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true}))
	t.Cleanup(endpoint.Close)

	for _, test := range []struct{ name, target string }{{"node", TargetTS}, {"browser", TargetUI}, {"python", TargetPy}} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			app := t.TempDir()
			dependencies := os.Getenv("HOLON_SDK_TEST_ENV")
			if dependencies == "" {
				dependencies = "../../tooling/typescript"
				if test.target == TargetPy {
					dependencies = "../../tooling/python/.venv"
				}
			}
			dependencies, err := filepath.Abs(dependencies)
			if err != nil {
				t.Fatal(err)
			}
			var program string
			if test.target == TargetPy {
				if err := os.Symlink(dependencies, filepath.Join(app, ".venv")); err != nil {
					t.Fatal(err)
				}
				program = filepath.Join(dependencies, "bin/python")
				put(t, app, "pyproject.toml", "[project]\nname = \"caller\"\nversion = \"1\"\ndependencies = [\"holon-sh\"]\n")
			} else {
				program = "node"
				if err := os.Symlink(filepath.Join(dependencies, "node_modules"), filepath.Join(app, "node_modules")); err != nil {
					t.Fatal(err)
				}
				put(t, app, "package.json", fmt.Sprintf(`{"type":"module","dependencies":{%q:"*"}}`, NodePackages[test.target]))
				put(t, app, "tsconfig.json", fmt.Sprintf(consumerTSConfig, false))
			}
			consumer := Consumer{Name: "caller", Dir: app, Targets: []string{test.target}, Sources: []Source{{Document: document(t, orders)}}}
			if err := Generate(t.Context(), []Consumer{consumer}, Options{}); err != nil {
				t.Fatal(err)
			}
			if test.target == TargetPy {
				put(t, app, "main.py", `from holon.apps import create_apps
apps = create_apps({"orders": {"endpoint": "https://orders.example/mcp"}})
async def use() -> float:
    result = await apps.orders.tools.lookup(id="1")
    async with apps.orders.ext.events.subscribe("cells.edited", {"notebook": "1"}) as changes:
        async for event in changes:
            for edit in event.data["edits"]:
                if edit["op"] == "cell.updated":
                    code: str = edit["code"]
                else:
                    id: str = edit["id"]
    return result["total"]
`)
			} else {
				put(t, app, "src/index.ts", fmt.Sprintf(`import { createApps, type EndpointBinding } from "#%s";
const binding: EndpointBinding = { endpoint: "https://orders.example/mcp", fetch };
const apps = createApps({ orders: binding });
for await (const { data } of await apps.orders.ext.events.subscribe("cells.edited", { notebook: "1" })) {
  for (const edit of data.edits) {
    if (edit.op === "cell.updated") { const code: string = edit.code; }
    else { const id: string = edit.id; }
  }
}
export const total: Promise<number> = apps.orders.tools.lookup({ id: "1" }).then(result => result.total);
`, NodePackages[test.target]))
				if test.target == TargetTS {
					put(t, app, "src/session.ts", `import { createApps } from "#holon-sh";
createApps({ orders: { endpoint: "mcp:" } }, { session: () => ({
  callTool: async () => ({ content: [], structuredContent: { total: 1 } }),
  readResource: async () => ({ contents: [] }),
  listTools: async () => ({ tools: [] }),
}) });
`)
				}
			}
			if output, err := check(t, app); err != nil {
				t.Fatalf("installed SDK rejects generated client: %v\n%s", err, output)
			}
			if test.target == TargetPy {
				put(t, app, "main.py", `from holon.apps import apps
async def use() -> None:
    await apps.orders.tools.lookup(id=42)
    (await apps.orders.tools.lookup(id="1"))["missing"]
    apps.orders.ext.events.subscribe("cells.edit", {"notebook": "1"})
    apps.orders.ext.events.subscribe("cells.edited", {"notbook": "1"})
    async with apps.orders.ext.events.subscribe("cells.edited", {"notebook": "1"}) as changes:
        async for event in changes:
            event.data["edits"][0]["code"]
`)
				mustReject(t, app, "incompatible type", `has no key "missing"`, `Literal['cells.edit']`, `Extra key "notbook"`, `has no key "code"`)
				put(t, app, "run.py", fmt.Sprintf(`import asyncio
from holon.apps import create_apps
async def main():
    apps = create_apps({"orders": {"endpoint": %q}})
    assert await apps.orders.tools.lookup(id="valid") == {"total": 1}
    try:
        await apps.orders.tools.lookup(id="bad")
    except ValueError as error:
        assert "structured output" in str(error), str(error)
    else:
        raise AssertionError("accepted a response that breaks the generated contract")
asyncio.run(main())
`, endpoint.URL))
				runSDKConsumer(t, app, program, "run.py")
			} else {
				put(t, app, "tsconfig.json", fmt.Sprintf(consumerTSConfig, true))
				prefix := fmt.Sprintf(`import { createApps } from "#%s";
const apps = createApps({ orders: { endpoint: "https://orders.example/mcp" } });
`, NodePackages[test.target])
				put(t, app, "src/index.ts", prefix+`apps.orders.tools.lookup({ id: 42 });
(await apps.orders.tools.lookup({ id: "1" })).missing;
apps.orders.ext.events.subscribe("cells.edit", { notebook: "1" });
apps.orders.ext.events.subscribe("cells.edited", { notbook: "1" });
for await (const { data } of await apps.orders.ext.events.subscribe("cells.edited", { notebook: "1" })) data.edits[0]!.code;
createApps({ orders: { endpoint: "mcp:", fetsh: fetch } });
`)
				mustReject(t, app, "not assignable to type 'string'", "Property 'missing' does not exist", `"cells.edit"`, "'notbook' does not exist", "Property 'code' does not exist", "'fetsh' does not exist")
				put(t, app, "run.mjs", fmt.Sprintf(`import assert from "node:assert/strict";
import { createApps } from "#%s";
const apps = createApps({ orders: { endpoint: %q } });
try {
  assert.deepEqual(await apps.orders.tools.lookup({ id: "valid" }), { total: 1 });
  await assert.rejects(apps.orders.tools.lookup({ id: "bad" }), /structured output/);
} finally { await apps.orders.close(); }
`, NodePackages[test.target], endpoint.URL))
				runSDKConsumer(t, app, program, "run.mjs")
			}
		})
	}
}

func runSDKConsumer(t *testing.T, app, program, file string) {
	t.Helper()
	command := exec.CommandContext(t.Context(), program, file)
	command.Dir = app
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generated runtime client: %v\n%s", err, output)
	}
}
