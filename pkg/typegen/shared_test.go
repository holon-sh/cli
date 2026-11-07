package typegen

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"github.com/holon-sh/cli/pkg/mcpcontract"
)

// Tools stat and list declare the same definitions: inputs take a Filter, and outputs return a
// recursive Entry that holds an Owner. Their objects are closed, so that every language checks
// their fields and the Go client has structs.
const (
	filterDefinitions = `"$defs":{"Filter":{"type":"object","additionalProperties":false,"required":["pattern"],"properties":{"pattern":{"type":"string"}}}}`
	entryDefinitions  = `"$defs":{"Entry":{"type":"object","additionalProperties":false,"required":["name","owner"],"properties":{"name":{"type":"string"},"owner":{"$ref":"#/$defs/Owner"},"children":{"type":"array","items":{"$ref":"#/$defs/Entry"}}}},"Owner":{"type":"object","additionalProperties":false,"required":["login"],"properties":{"login":{"type":"string"}}}}`
)

func generateShared(t *testing.T, app, target string) {
	t.Helper()
	tool := func(name, input, output string) mcpcontract.Tool {
		return mcpcontract.Tool{Name: name,
			Input:  json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{` + input + `},` + filterDefinitions + `}`),
			Output: json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{` + output + `},` + entryDefinitions + `}`)}
	}
	source := Source{Document: document(t, &mcpcontract.Metadata{
		Identity: mcpcontract.Identity{Name: "example.com/files", Version: "1.0.0"},
		Schema: &mcpcontract.Contract{Tools: []mcpcontract.Tool{
			tool("stat", `"path":{"type":"string"},"filter":{"$ref":"#/$defs/Filter"}`, `"entry":{"$ref":"#/$defs/Entry"}`),
			tool("list", `"filter":{"$ref":"#/$defs/Filter"}`, `"entries":{"type":"array","items":{"$ref":"#/$defs/Entry"}}`),
		}},
	})}
	if err := Generate(t.Context(), []Consumer{{Name: "caller", Dir: app, Targets: []string{target}, Sources: []Source{source}}}, Options{}); err != nil {
		t.Fatal(err)
	}
}

// declaredOnce fails unless each field is declared exactly once in the generated types.
func declaredOnce(t *testing.T, types string, fields ...string) {
	t.Helper()
	for _, field := range fields {
		if count := strings.Count(types, field); count != 1 {
			t.Errorf("%s is declared %d times, want once:\n%s", field, count, types)
		}
	}
}

func TestSharedDefinitionsAreDeclaredOnce(t *testing.T) {
	t.Run("typescript", func(t *testing.T) {
		t.Parallel()
		app := typeScriptApp(t)
		put(t, app, "tsconfig.json", `{"compilerOptions":{"strict":true,"noEmit":true,"skipLibCheck":false,"target":"ES2023","module":"NodeNext","moduleResolution":"NodeNext"},"include":["src"]}`)
		generateShared(t, app, TargetTS)
		declaredOnce(t, read(t, app, "node_modules/holon-sh/generated/index.d.ts"), `"pattern": string`, `"login": string`, `"children"?:`)
		put(t, app, "src/index.ts", `import type { Inputs, Outputs, OutputTypes } from "holon-sh";
declare const stat: Outputs["files"]["stat"];
declare const listed: Outputs["files"]["list"];
export const entry: OutputTypes["files"]["stat"]["Entry"] = listed.entries![0]!;
export const login: string = stat.entry!.children![0]!.owner.login;
export const input: Inputs["files"]["list"] = { filter: { pattern: "*" } };
`)
		if output, err := check(t, app); err != nil {
			t.Fatalf("check: %v\n%s", err, output)
		}
		put(t, app, "src/index.ts", `import type { Inputs, Outputs } from "holon-sh";
declare const listed: Outputs["files"]["list"];
export const login = listed.entries![0]!.children![0]!.owner.lgoin;
export const input: Inputs["files"]["stat"] = { filter: { patern: "*" } };
`)
		mustReject(t, app, "Property 'lgoin' does not exist", "'patern' does not exist")
	})
	t.Run("python", func(t *testing.T) {
		t.Parallel()
		app := t.TempDir()
		python := pythonEnvironment(app, needPython(t))
		if exec.CommandContext(t.Context(), python, "-c", "import mypy").Run() != nil {
			t.Skip("mypy is not installed")
		}
		put(t, app, "pyproject.toml", "[project]\nname = \"caller\"\nversion = \"1\"\ndependencies = [\"holon-sh\"]\n")
		generateShared(t, app, TargetPy)
		declaredOnce(t, read(t, app, "holon/apps/__init__.py"), `"pattern": _builtins.str`, `"login": _builtins.str`, `"children": `)
		put(t, app, "holon/client.py", `from typing import Any, Generic, TypeVar
T = TypeVar("T")
class Apps:
    def __init__(self, *args: Any) -> None: ...
class Binding: ...
class CallOptions: ...
class CompleteResult(Generic[T]): ...
class ToolResult(Generic[T]): ...
`)
		put(t, app, "main.py", `from holon.apps import Outputs, OutputTypes, apps
async def use(stat: Outputs.files.stat, listed: Outputs.files.list) -> None:
    entry: OutputTypes.files.stat.Entry = listed["entries"][0]
    login: str = stat["entry"]["children"][0]["owner"]["login"]
    await apps.files.tools.list(filter={"pattern": "*"})
`)
		if output, err := check(t, app); err != nil {
			t.Fatalf("check: %v\n%s", err, output)
		}
		put(t, app, "main.py", `from holon.apps import Outputs, apps
async def use(listed: Outputs.files.list) -> None:
    listed["entries"][0]["children"][0]["owner"]["lgoin"]
    await apps.files.tools.stat(filter={"patern": "*"})
`)
		mustReject(t, app, `has no key "lgoin"`, `"patern"`)
	})
	t.Run("go", func(t *testing.T) {
		t.Parallel()
		app := t.TempDir()
		put(t, app, "go.mod", "module example.com/caller\n\ngo 1.27.0\n")
		generateShared(t, app, TargetGo)
		declaredOnce(t, read(t, app, "holon/types.go"), `json:\"pattern\"`, `json:\"login\"`, `json:\"children,omitempty\"`)
		// Go types are nominal, so this compiles only if both tools name the same types.
		put(t, app, "holon/use_test.go", `package holon

import (
	"encoding/json"
	"testing"
)

func login(entry FilesStatOutputEntry) string { return entry.Owner.Login }

func TestToolsShareTypes(t *testing.T) {
	var listed FilesListOutput
	if err := json.Unmarshal([]byte(`+"`"+`{"entries": [{"name": "a", "owner": {"login": "x"}, "children": [{"name": "b", "owner": {"login": "y"}}]}]}`+"`"+`), &listed); err != nil {
		t.Fatal(err)
	}
	stat := FilesStatOutput{Entry: &(*listed.Entries)[0]}
	if name := login((*stat.Entry.Children)[0]); name != "y" {
		t.Fatalf("login = %q", name)
	}
	var filter FilesStatInputFilter = FilesListInputFilter{Pattern: "*"}
	_ = FilesListInput{Filter: &filter}
}
`)
		compile := exec.CommandContext(t.Context(), "go", "test", "holon/types.go", "holon/use_test.go")
		compile.Dir = app
		if output, err := compile.CombinedOutput(); err != nil {
			t.Fatalf("generated types do not compile and decode: %v\n%s", err, output)
		}
	})
}
