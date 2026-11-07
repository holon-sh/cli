package typegen

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"github.com/holon-sh/cli/pkg/mcpcontract"
)

// recursiveSchema closes its objects, so that the Go client has structs to walk. A compiler that
// rejects any of its odd entries rejects the whole generated client: Knot and Tangle reach themselves
// through nothing that can be declared, and Wrap only reaches a type that does. The rest of Pair
// needs its own spelling in TypeScript, Spiral merges with itself, which Python can only do by
// inlining, and Ring requires itself, which a Go struct cannot hold by value. Chain only names Link,
// so Link is the type declared.
const recursiveSchema = `{"type":"object","additionalProperties":false,"required":["root"],"properties":{
	"root":{"$ref":"#/$defs/FileInfo"},
	"tree":{"$ref":"#/$defs/Tree"},
	"rows":{"$ref":"#/$defs/Rows"},
	"index":{"$ref":"#/$defs/Index"},
	"left":{"$ref":"#/$defs/Left"},
	"wrapped":{"allOf":[{"$ref":"#/$defs/FileInfo"}]},
	"optional":{"anyOf":[{"$ref":"#/$defs/FileInfo"},{"type":"null"}]},
	"extended":{"allOf":[{"$ref":"#/$defs/FileInfo"},{"type":"object","properties":{"size":{"type":"integer"}}}]},
	"pair":{"$ref":"#/$defs/Pair"},
	"knot":{"$ref":"#/$defs/Wrap"},
	"tangle":{"$ref":"#/$defs/TangleA"},
	"spiral":{"$ref":"#/$defs/Spiral"},
	"ring":{"$ref":"#/$defs/Ring"},
	"chain":{"$ref":"#/$defs/Chain"},
	"self":{"$ref":"#"}},
"$defs":{
	"FileInfo":{"type":"object","additionalProperties":false,"required":["name"],"properties":{"name":{"type":"string"},"children":{"type":"array","items":{"$ref":"#/$defs/FileInfo"}}}},
	"Tree":{"anyOf":[{"type":"string"},{"type":"array","items":{"$ref":"#/$defs/Tree"}}]},
	"Rows":{"type":"array","items":{"$ref":"#/$defs/Rows"}},
	"Index":{"type":"object","additionalProperties":{"$ref":"#/$defs/Index"}},
	"Left":{"type":"object","additionalProperties":false,"required":["right"],"properties":{"right":{"$ref":"#/$defs/Right"}}},
	"Right":{"type":"object","additionalProperties":false,"properties":{"left":{"$ref":"#/$defs/Left"}}},
	"Pair":{"type":"array","prefixItems":[{"type":"string"}],"items":{"$ref":"#/$defs/Pair"}},
	"Wrap":{"anyOf":[{"$ref":"#/$defs/Knot"}]},
	"Knot":{"anyOf":[{"$ref":"#/$defs/Knot"},{"type":"string"}]},
	"TangleA":{"anyOf":[{"type":"object","properties":{"x":{"$ref":"#/$defs/TangleB"}}},{"$ref":"#/$defs/TangleB"}]},
	"TangleB":{"anyOf":[{"$ref":"#/$defs/TangleA"},{"type":"object","properties":{"y":{"$ref":"#/$defs/TangleB"}}}]},
	"Spiral":{"type":"object","additionalProperties":false,"properties":{"next":{"allOf":[{"$ref":"#/$defs/Spiral"},{"type":"object","properties":{"extra":{"type":"string"}}}]}}},
	"Ring":{"type":"object","additionalProperties":false,"required":["next"],"properties":{"next":{"$ref":"#/$defs/Ring"}}},
	"Chain":{"$ref":"#/$defs/Link"},
	"Link":{"type":"object","additionalProperties":false,"properties":{"chain":{"$ref":"#/$defs/Chain"},"next":{"$ref":"#/$defs/Link"}}}}}`

// openFileInfo has the common shape of a recursive output: its objects stay open, so the Go client
// reads them as maps.
const openFileInfo = `"$defs":{"FileInfo":{"type":"object","required":["name"],"properties":{"name":{"type":"string"},"children":{"type":"array","items":{"$ref":"#/$defs/FileInfo"}}}}}`

// generateRecursive writes a client for recursiveSchema as tool walk, openFileInfo as tool list and
// as a resource, and as tool nest a keyword input that is a map of itself, which Python cannot
// unpack as a TypedDict.
func generateRecursive(t *testing.T, app, target string) {
	t.Helper()
	source := Source{Document: document(t, &mcpcontract.Metadata{
		Identity: mcpcontract.Identity{Name: "example.com/files", Version: "1.0.0"},
		Schema: &mcpcontract.Contract{
			Tools: []mcpcontract.Tool{
				{Name: "walk", Input: json.RawMessage(recursiveSchema), Output: json.RawMessage(recursiveSchema)},
				{Name: "list", Input: json.RawMessage(`{"type":"object","additionalProperties":false}`), Output: json.RawMessage(`{"type":"object","required":["files"],"properties":{"files":{"type":"array","items":{"$ref":"#/$defs/FileInfo"}}},` + openFileInfo + `}`)},
				{Name: "nest", Input: json.RawMessage(`{"type":"object","additionalProperties":{"anyOf":[{"$ref":"#"},{"type":"null"}]}}`)},
			},
			Resources: []mcpcontract.Resource{{URI: "files://root", Output: json.RawMessage(`{"$ref":"#/$defs/FileInfo",` + openFileInfo + `}`)}},
		},
	})}
	if err := Generate(t.Context(), []Consumer{{Name: "caller", Dir: app, Targets: []string{target}, Sources: []Source{source}}}, Options{}); err != nil {
		t.Fatal(err)
	}
}

// check runs holon types check on app.
func check(t *testing.T, app string) (string, error) {
	var output bytes.Buffer
	err := Check(t.Context(), app, Options{Stdout: &output, Stderr: &output})
	return output.String(), err
}

// mustReject fails unless holon types check rejects app, reporting every expected diagnostic.
func mustReject(t *testing.T, app string, diagnostics ...string) {
	t.Helper()
	output, err := check(t, app)
	if err == nil {
		t.Fatalf("accepted wrong uses at depth:\n%s", output)
	}
	for _, diagnostic := range diagnostics {
		if !strings.Contains(output, diagnostic) {
			t.Errorf("rejected without %q: %v\n%s", diagnostic, err, output)
		}
	}
}

func TestRecursiveTypesStayPreciseAtAnyDepth(t *testing.T) {
	t.Run("typescript", func(t *testing.T) {
		t.Parallel()
		app := typeScriptApp(t)
		// Unlike the runner's own flags, these check the generated declarations themselves, and
		// that a project publishing declarations can export an inferred recursive type.
		put(t, app, "tsconfig.json", `{"compilerOptions":{"strict":true,"noEmit":true,"declaration":true,"skipLibCheck":false,"target":"ES2023","module":"NodeNext","moduleResolution":"NodeNext"},"include":["src"]}`)
		generateRecursive(t, app, TargetTS)
		put(t, app, "src/index.ts", `import type { Inputs, Outputs, OutputTypes, Resources } from "holon-sh";
type Walk = Outputs["files"]["walk"];
declare const walk: Walk;
declare const listed: Outputs["files"]["list"];
declare const resource: Resources["files"]["files://root"];
export const name: string = walk.root.children![0]!.children![0]!.name;
export const read: string = resource.children![0]!.children![0]!.name;
export const inferred = [walk.root, listed.files, resource];
export const indexed: NonNullable<Walk["root"]["children"]>[number]["name"] = "a";
export const named: OutputTypes["files"]["walk"]["FileInfo"] = walk.root;
export const wrapped: string = walk.wrapped!.children![0]!.name;
export const optional: string | undefined = walk.optional?.children?.[0]?.name;
export const extended: number | undefined = walk.extended!.size;
export const tree: Walk["tree"] = ["leaf", ["leaf", ["leaf"]]];
export const rows: Walk["rows"] = [[[]]];
export const index: Walk["index"] = { a: { b: {} } };
export const mutual: Walk["left"] = { right: { left: { right: {} } } };
export const pair: Walk["pair"] = ["a", ["b", ["c"]]];
export const self: string = walk.self!.self!.root.name;
export const input: Inputs["files"]["walk"] = { root: { name: "a", children: [{ name: "b", children: [{ name: "c" }] }] } };
`)
		if output, err := check(t, app); err != nil {
			t.Fatalf("check: %v\n%s", err, output)
		}
		put(t, app, "src/index.ts", `import type { Inputs, Outputs } from "holon-sh";
type Walk = Outputs["files"]["walk"];
declare const walk: Walk;
declare const listed: Outputs["files"]["list"];
export const nam = walk.root.children![0]!.children![0]!.nam;
export const nme = walk.self!.self!.root.children![0]!.children![0]!.nme;
export const rihgt = walk.left!.right.left!.right.left!.rihgt;
export const open = listed.files[0]!.children![0]!.children![0]!.name satisfies 1;
export const pair: Walk["pair"] = ["a", ["b", [1]]];
export const input: Inputs["files"]["walk"] = { root: { name: "a", children: [{ name: "b", children: [{ nma: "c" }] }] } };
`)
		mustReject(t, app,
			"Property 'nam' does not exist", "Property 'nme' does not exist", "Property 'rihgt' does not exist",
			"does not satisfy the expected type", "Type 'number' is not assignable", "'nma' does not exist")
	})
	t.Run("python", func(t *testing.T) {
		t.Parallel()
		app := t.TempDir()
		python := pythonEnvironment(app, needPython(t))
		if exec.CommandContext(t.Context(), python, "-c", "import mypy").Run() != nil {
			t.Skip("mypy is not installed")
		}
		put(t, app, "pyproject.toml", "[project]\nname = \"caller\"\nversion = \"1\"\ndependencies = [\"holon-sh\"]\n")
		generateRecursive(t, app, TargetPy)
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
async def use(walk: Outputs.files.walk, listed: Outputs.files.list, named: OutputTypes.files.walk.FileInfo) -> None:
    name: str = walk["root"]["children"][0]["children"][0]["name"]
    open_name: str = listed["files"][0]["children"][0]["children"][0]["name"]
    named_name: str = named["children"][0]["name"]
    wrapped: str = walk["wrapped"]["children"][0]["name"]
    optional = walk["optional"]
    optional_name: str = optional["children"][0]["name"] if optional else ""
    size: int = walk["extended"]["size"]
    tree: Outputs.files.walk = {"root": {"name": "a"}, "tree": ["leaf", ["leaf", ["leaf"]]], "rows": [[[]]], "index": {"a": {"b": {}}}, "left": {"right": {"left": {"right": {}}}}}
    self_name: str = walk["self"]["self"]["root"]["name"]
    await apps.files.tools.walk(root={"name": "a", "children": [{"name": "b", "children": [{"name": "c"}]}]})
    await apps.files.tools.nest(a={"b": None})
`)
		if output, err := check(t, app); err != nil {
			t.Fatalf("check: %v\n%s", err, output)
		}
		// mypy reads the module without running it, but Python evaluates most of its types on import,
		// and a caller's annotations too unless it postpones them.
		load := exec.CommandContext(t.Context(), python, "-c", "import holon.apps as a; a.Outputs.files.walk | None; a.OutputTypes.files.walk.Tree | None; a.OutputTypes.files.walk.Chain | None")
		load.Dir = app
		if output, err := load.CombinedOutput(); err != nil {
			t.Fatalf("generated module failed to import: %v\n%s", err, output)
		}
		put(t, app, "main.py", `from holon.apps import Outputs, apps
async def use(walk: Outputs.files.walk, listed: Outputs.files.list) -> None:
    walk["root"]["children"][0]["children"][0]["nam"]
    listed["files"][0]["children"][0]["children"][0]["nme"]
    walk["left"]["right"]["left"]["right"]["rihgt"]
    await apps.files.tools.walk(root={"name": "a", "children": [{"name": "b", "children": [{"nma": "c"}]}]})
    await apps.files.tools.nest(a=1)
`)
		mustReject(t, app, `has no key "nam"`, `has no key "nme"`, `has no key "rihgt"`, `"nma"`, `Argument "a" to "nest"`)
	})
	t.Run("go", func(t *testing.T) {
		t.Parallel()
		app := t.TempDir()
		put(t, app, "go.mod", "module example.com/caller\n\ngo 1.27.0\n")
		generateRecursive(t, app, TargetGo)
		put(t, app, "holon/use_test.go", `package holon

import (
	"encoding/json"
	"testing"
)

func TestDecodesEveryDepth(t *testing.T) {
	var walk FilesWalkOutput
	data := `+"`"+`{"root": {"name": "a", "children": [{"name": "b", "children": [{"name": "c"}]}]},
		"tree": ["leaf", ["leaf", ["leaf"]]], "rows": [[[]]], "index": {"a": {"b": {}}},
		"left": {"right": {"left": {"right": {}}}}, "wrapped": {"name": "d"}}`+"`"+`
	if err := json.Unmarshal([]byte(data), &walk); err != nil {
		t.Fatal(err)
	}
	if name := (*(*walk.Root.Children)[0].Children)[0].Name; name != "c" {
		t.Fatalf("name = %q", name)
	}
	if walk.Left.Right.Left.Right.Left != nil || len((*walk.Rows)[0]) != 1 || (*walk.Index)["a"]["b"] == nil || walk.Wrapped.Name != "d" {
		t.Fatalf("decoded %+v", walk)
	}
	// Unions decode as raw JSON, as they do when not recursive, and so does their exported name.
	var tree FilesWalkOutputTree
	if err := json.Unmarshal(walk.Tree, &tree); err != nil || string(tree) != `+"`"+`["leaf", ["leaf", ["leaf"]]]`+"`"+` {
		t.Fatalf("tree = %s, %v", tree, err)
	}
	var named FilesWalkOutputFileInfo = *walk.Root
	var input FilesWalkInput
	var inputRoot *FilesWalkInputFileInfo = input.Root
	var self *FilesWalkOutputValue = walk.Self
	_, _, _ = named, inputRoot, self
}
`)
		// holon types check leaves Go to the compiler. Only clients.go needs the SDK. Go has no
		// dynamic member access, so reading Name at depth also proves a misspelling would not compile.
		compile := exec.CommandContext(t.Context(), "go", "test", "holon/types.go", "holon/use_test.go")
		compile.Dir = app
		if output, err := compile.CombinedOutput(); err != nil {
			t.Fatalf("generated types do not compile and decode: %v\n%s", err, output)
		}
	})
}
