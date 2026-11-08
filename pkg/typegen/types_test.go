package typegen

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/holon-sh/cli/pkg/mcpcontract"
)

// fileInfo is a recursive entry holding an owner. Tools walk and stat both define it, in their inputs
// and their outputs.
const fileInfo = `"FileInfo":{"type":"object","additionalProperties":false,"required":["name"],"properties":{"name":{"type":"string"},"owner":{"$ref":"#/$defs/Owner"},"children":{"type":"array","items":{"$ref":"#/$defs/FileInfo"}}}},
	"Owner":{"type":"object","additionalProperties":false,"required":["login"],"properties":{"login":{"type":"string"}}}`

// A compiler that rejects any of recursiveSchema's odd entries rejects the whole generated client:
// Knot and Tangle reach themselves through nothing that can be declared, and Wrap only reaches a
// type that does. The rest of Pair needs its own spelling in TypeScript, Spiral merges with itself,
// which Python can only do by inlining, and Ring requires itself, which a Go struct cannot hold by
// value. Chain only names Link, so Link is the type declared.
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
"$defs":{` + fileInfo + `,
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

// statInput does not declare additional properties, which closes an input that names its fields.
// Two of its objects are open: one names a field and allows others, and one names none.
const statInput = `{"type":"object","properties":{"file":{"$ref":"#/$defs/FileInfo"},
	"open":{"type":"object","properties":{"state":{"type":"string"}},"additionalProperties":true},"free":{"type":"object"}},"$defs":{` + fileInfo + `}}`

// openFileInfo has the common shape of a recursive output: its objects name fields without declaring
// additionalProperties, and render as closed ones do.
const openFileInfo = `"$defs":{"FileInfo":{"type":"object","required":["name"],"properties":{"name":{"type":"string"},"children":{"type":"array","items":{"$ref":"#/$defs/FileInfo"}}}}}`

// openFileTree is openFileInfo's FileInfo as a whole output.
const openFileTree = `{"$ref":"#/$defs/FileInfo",` + openFileInfo + `}`

// generateFiles writes a client for recursiveSchema as tool walk, statInput as tool stat, openFileInfo
// as tool list and as two resources, and as tool nest a keyword input that is a map of itself, which
// Python cannot unpack as a TypedDict. The resources share their FileInfo as walk and stat share
// theirs, so the two declarations would take the same name.
func generateFiles(t *testing.T, app, target string) {
	t.Helper()
	source := Source{Document: document(t, &mcpcontract.Metadata{
		Identity: mcpcontract.Identity{Name: "example.com/files", Version: "1.0.0"},
		Schema: &mcpcontract.Contract{
			Tools: []mcpcontract.Tool{
				{Name: "walk", Input: json.RawMessage(recursiveSchema), Output: json.RawMessage(recursiveSchema)},
				{Name: "stat", Input: json.RawMessage(statInput), Output: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["file"],"properties":{"file":{"$ref":"#/$defs/FileInfo"}},"$defs":{` + fileInfo + `}}`)},
				{Name: "list", Input: json.RawMessage(`{"type":"object","additionalProperties":false}`), Output: json.RawMessage(`{"type":"object","required":["files"],"properties":{"files":{"type":"array","items":{"$ref":"#/$defs/FileInfo"}}},` + openFileInfo + `}`)},
				{Name: "nest", Input: json.RawMessage(`{"type":"object","additionalProperties":{"anyOf":[{"$ref":"#"},{"type":"null"}]}}`)},
			},
			Resources: []mcpcontract.Resource{
				{URI: "files://root", Output: json.RawMessage(openFileTree)},
				{URI: "files://home", Output: json.RawMessage(openFileTree)},
			},
		},
	})}
	if err := Generate(t.Context(), []Consumer{{Name: "caller", Dir: app, Targets: []string{target}, Sources: []Source{source}}}, Options{}); err != nil {
		t.Fatal(err)
	}
}

// declaredOnce fails unless the shared Owner, named by its field, is declared once in inputs and
// once in outputs, although walk and stat both define it.
func declaredOnce(t *testing.T, types, field string) {
	t.Helper()
	if count := strings.Count(types, field); count != 2 {
		t.Errorf("%s is declared %d times, want once per direction:\n%s", field, count, types)
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
	if err == nil || !strings.Contains(err.Error(), "does not type-check") {
		t.Fatalf("accepted wrong uses: %v\n%s", err, output)
	}
	for _, diagnostic := range diagnostics {
		if !strings.Contains(output, diagnostic) {
			t.Errorf("rejected without %q: %v\n%s", diagnostic, err, output)
		}
	}
}

// Generated clients take the runtime's binding and option types: with the published holon-sh
// declarations, a session hook and a plain fetch bind, and a binding's fields are still checked.
func TestGeneratedClientsBindAsTheRuntimeDoes(t *testing.T) {
	t.Parallel()
	app := typeScriptApp(t)
	for _, name := range []string{"runtime.d.ts", "protocol.d.ts"} {
		put(t, filepath.Join(app, "node_modules/holon-sh"), name, read(t, "testdata/holon-sh", name))
	}
	c := Consumer{Name: "caller", Dir: app, Targets: []string{TargetTS}, Sources: []Source{{Document: document(t, ordersMetadata())}}}
	if err := Generate(t.Context(), []Consumer{c}, Options{}); err != nil {
		t.Fatal(err)
	}
	put(t, app, "src/index.ts", `import { createApps, type EndpointBinding } from "holon-sh";
const binding: EndpointBinding = { endpoint: "mcp:", fetch };
const apps = createApps({ orders: binding }, { session: () => ({
  callTool: async () => ({ structuredContent: { total: 1 } }),
  readResource: async () => ({ contents: [] }),
  listTools: async () => ({ tools: [] }),
}) });
export const total: Promise<number> = apps.orders.tools.lookup({ id: "1" }).then(result => result.total);
`)
	if output, err := check(t, app); err != nil {
		t.Fatalf("check: %v\n%s", err, output)
	}
	put(t, app, "src/index.ts", `import { createApps } from "holon-sh";
export const misspelled = createApps({ orders: { endpoint: "mcp:", fetsh: fetch } });
`)
	mustReject(t, app, "'fetsh' does not exist")
}

func TestGeneratedTypesArePreciseAtAnyDepth(t *testing.T) {
	t.Run("typescript", func(t *testing.T) {
		t.Parallel()
		app := typeScriptApp(t)
		// Unlike the runner's own flags, these check the generated declarations themselves, and
		// that a project publishing declarations can export an inferred recursive type, a resource's too.
		put(t, app, "tsconfig.json", `{"compilerOptions":{"strict":true,"noEmit":true,"declaration":true,"skipLibCheck":false,"target":"ES2023","module":"NodeNext","moduleResolution":"NodeNext"},"include":["src"]}`)
		generateFiles(t, app, TargetTS)
		declaredOnce(t, read(t, app, "node_modules/holon-sh/generated/index.d.ts"), `"login": string`)
		put(t, app, "src/index.ts", `import type { Inputs, Outputs, OutputTypes, Resources } from "holon-sh";
type Walk = Outputs["files"]["walk"];
declare const walk: Walk;
declare const stat: Outputs["files"]["stat"];
declare const listed: Outputs["files"]["list"];
declare const resource: Resources["files"]["files://root"];
declare const summary: Omit<OutputTypes["files"]["list"]["FileInfo"], "children">;
export const name: string = walk.root.children![0]!.children![0]!.name;
export const read: string = resource.children![0]!.children![0]!.name;
export const login: string = stat.file.children![0]!.owner!.login;
export const inferred = [walk.root, listed.files];
export const exported = resource;
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
export const summarized: string = summary.name;
export const input: Inputs["files"]["walk"] = { root: { name: "a", children: [{ name: "b", children: [{ name: "c" }] }] } };
export const open: Inputs["files"]["stat"] = { file: { name: "a", owner: { login: "b" } }, open: { other: 1 }, free: { other: 1 } };
`)
		if output, err := check(t, app); err != nil {
			t.Fatalf("check: %v\n%s", err, output)
		}
		put(t, app, "src/index.ts", `import type { Inputs, InputTypes, Outputs } from "holon-sh";
type Walk = Outputs["files"]["walk"];
declare const walk: Walk;
declare const stat: Outputs["files"]["stat"];
declare const listed: Outputs["files"]["list"];
export const nam = walk.root.children![0]!.children![0]!.nam;
export const nme = walk.self!.self!.root.children![0]!.children![0]!.nme;
export const rihgt = walk.left!.right.left!.right.left!.rihgt;
export const lgin = stat.file.children![0]!.owner!.lgin;
export const open = listed.files[0]!.children![0]!.children![0]!.name satisfies 1;
export const extra: Outputs["files"]["list"] = { files: [], other: true };
export const pair: Walk["pair"] = ["a", ["b", [1]]];
export const input: Inputs["files"]["walk"] = { root: { name: "a", children: [{ name: "b", children: [{ nma: "c" }] }] } };
export const top: Inputs["files"]["stat"] = { fil: { name: "a" } };
export const owner: InputTypes["files"]["stat"]["Owner"] = { lgn: "a" };
`)
		mustReject(t, app,
			"Property 'nam' does not exist", "Property 'nme' does not exist", "Property 'rihgt' does not exist", "Property 'lgin' does not exist",
			"does not satisfy the expected type", "'other' does not exist", "Type 'number' is not assignable", "'nma' does not exist", "'fil' does not exist", "'lgn' does not exist")
	})
	t.Run("python", func(t *testing.T) {
		t.Parallel()
		app := t.TempDir()
		python := pythonEnvironment(app, needPython(t))
		if exec.CommandContext(t.Context(), python, "-c", "import mypy").Run() != nil {
			t.Skip("mypy is not installed")
		}
		put(t, app, "pyproject.toml", "[project]\nname = \"caller\"\nversion = \"1\"\ndependencies = [\"holon-sh\"]\n")
		generateFiles(t, app, TargetPy)
		declaredOnce(t, read(t, app, "holon/apps/__init__.py"), `"login": _builtins.str`)
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
async def use(walk: Outputs.files.walk, stat: Outputs.files.stat, listed: Outputs.files.list, named: OutputTypes.files.walk.FileInfo) -> None:
    name: str = walk["root"]["children"][0]["children"][0]["name"]
    login: str = stat["file"]["children"][0]["owner"]["login"]
    open_name: str = listed["files"][0]["children"][0]["children"][0]["name"]
    named_name: str = named["children"][0]["name"]
    wrapped: str = walk["wrapped"]["children"][0]["name"]
    optional = walk["optional"]
    optional_name: str = optional["children"][0]["name"] if optional else ""
    size: int = walk["extended"]["size"]
    tree: Outputs.files.walk = {"root": {"name": "a"}, "tree": ["leaf", ["leaf", ["leaf"]]], "rows": [[[]]], "index": {"a": {"b": {}}}, "left": {"right": {"left": {"right": {}}}}}
    self_name: str = walk["self"]["self"]["root"]["name"]
    await apps.files.tools.walk(root={"name": "a", "children": [{"name": "b", "children": [{"name": "c"}]}]})
    await apps.files.tools.stat(file={"name": "a", "owner": {"login": "b"}}, open={"other": 1}, free={"other": 1})
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
async def use(walk: Outputs.files.walk, stat: Outputs.files.stat, listed: Outputs.files.list) -> None:
    walk["root"]["children"][0]["children"][0]["nam"]
    listed["files"][0]["children"][0]["children"][0]["nme"]
    walk["left"]["right"]["left"]["right"]["rihgt"]
    stat["file"]["children"][0]["owner"]["lgin"]
    await apps.files.tools.walk(root={"name": "a", "children": [{"name": "b", "children": [{"nma": "c"}]}]})
    await apps.files.tools.stat(fil={"name": "a"})
    await apps.files.tools.nest(a=1)
`)
		mustReject(t, app, `has no key "nam"`, `has no key "nme"`, `has no key "rihgt"`, `has no key "lgin"`, `"nma"`, `Unexpected keyword argument "fil"`, `Argument "a" to "nest"`)
	})
	t.Run("go", func(t *testing.T) {
		t.Parallel()
		app := t.TempDir()
		put(t, app, "go.mod", "module example.com/caller\n\ngo 1.27.0\n")
		generateFiles(t, app, TargetGo)
		declaredOnce(t, read(t, app, "holon/types.go"), `json:\"login\"`)
		put(t, app, "holon/use_test.go", `package holon

import (
	"encoding/json"
	"testing"
)

func TestDecodesEveryDepth(t *testing.T) {
	var walk FilesWalkOutput
	data := `+"`"+`{"root": {"name": "a", "children": [{"name": "b", "children": [{"name": "c", "owner": {"login": "x"}}]}]},
		"tree": ["leaf", ["leaf", ["leaf"]]], "rows": [[[]]], "index": {"a": {"b": {}}},
		"left": {"right": {"left": {"right": {}}}}, "wrapped": {"name": "d"}}`+"`"+`
	if err := json.Unmarshal([]byte(data), &walk); err != nil {
		t.Fatal(err)
	}
	if file := (*(*walk.Root.Children)[0].Children)[0]; file.Name != "c" || file.Owner.Login != "x" {
		t.Fatalf("file = %+v", file)
	}
	if walk.Left.Right.Left.Right.Left != nil || len((*walk.Rows)[0]) != 1 || (*walk.Index)["a"]["b"] == nil || walk.Wrapped.Name != "d" {
		t.Fatalf("decoded %+v", walk)
	}
	var listed FilesListOutput
	if err := json.Unmarshal([]byte(`+"`"+`{"files": [{"name": "a", "children": [{"name": "b"}]}]}`+"`"+`), &listed); err != nil {
		t.Fatal(err)
	}
	if name := (*listed.Files[0].Children)[0].Name; name != "b" {
		t.Fatalf("name = %q", name)
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
	// Go types are nominal, so these compile only if walk and stat share their types.
	var file FilesStatOutputFileInfo = named
	_, _, _ = FilesStatOutput{File: &file}, FilesStatInput{File: inputRoot}, self
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
