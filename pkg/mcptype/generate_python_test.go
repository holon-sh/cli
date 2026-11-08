package mcptype

import (
	"encoding/json"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/holon-sh/cli/pkg/mcpcontract"
)

func TestPythonModulePassesItsIdentityToTheRuntime(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not installed")
	}
	identity := mcpcontract.Identity{Name: "a'b\"\\\n\r\t\x00\x1f\x7fé😀 ", Version: "1"}
	files, err := Generate(t.Context(), GenerateRequest{Target: "python", Identity: identity})
	if err != nil {
		t.Fatal(err)
	}
	// A stand-in runtime prints the identity the generated module constructs its clients with.
	root := t.TempDir()
	files["holon/client.py"] = []byte("import json\nclass Apps:\n    def __init__(self, identity, *rest):\n        print(json.dumps(identity))\ndef __getattr__(name):\n    return object\n")
	writeFiles(t, root, files)
	command := exec.CommandContext(t.Context(), python, "-c", "import holon.apps")
	command.Dir = root
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("generated module failed to import: %v\n%s", err, output)
	}
	var decoded mcpcontract.Identity
	if err := json.Unmarshal(output, &decoded); err != nil || decoded != identity {
		t.Fatalf("runtime received %+v, %v; want %+v", decoded, err, identity)
	}
}

// pythonWithMypy finds the active virtual environment's interpreter, or python3, and skips unless it
// imports mypy.
func pythonWithMypy(t *testing.T) string {
	t.Helper()
	python := filepath.Join(os.Getenv("VIRTUAL_ENV"), "bin", "python")
	if os.Getenv("VIRTUAL_ENV") == "" {
		var err error
		if python, err = exec.LookPath("python3"); err != nil {
			t.Skip("python3 is not installed")
		}
	}
	if exec.CommandContext(t.Context(), python, "-c", "import mypy").Run() != nil {
		t.Skip("mypy is not installed")
	}
	return python
}

// pythonRuntime stands in for holon.client: like the runtime, it names each tool's method after the
// tool, and its methods return the tool's name.
const pythonRuntime = `from __future__ import annotations
from types import SimpleNamespace
from typing import Any, Generic, Mapping, TypedDict, TypeVar
T = TypeVar("T")
class Binding(TypedDict, total=False):
    endpoint: str
class CallOptions(TypedDict, total=False):
    timeout: float
class CompleteResult(Generic[T]): ...
class ToolResult(Generic[T]): ...
def _method(tool: str) -> Any:
    async def call(*options: object, **arguments: object) -> str:
        return tool
    return call
class _App:
    def __init__(self, contract: dict[str, Any]) -> None:
        self.tools = SimpleNamespace(**{tool["name"]: _method(tool["name"]) for tool in contract["tools"] if not tool["name"].startswith("_")})
class Apps:
    def __init__(self, identity: dict[str, Any], contracts: dict[str, Any], bindings: Mapping[str, Binding] | None = None) -> None:
        self._apps = {name: _App(contract) for name, contract in contracts.items() if bindings is None or name in bindings}
    def __getitem__(self, name: str) -> _App:
        return self._apps[name]
    def __getattr__(self, name: str) -> _App:
        return self._apps[name]
`

func TestPythonMethodNames(t *testing.T) {
	tools := catalog{}
	for _, name := range []string{"get_order", "get-order", "order.list", "order/list", "runtime.run", "class", "123", "-", "_hidden"} {
		tools[name] = compiledTool{}
	}
	want := map[string]string{"get_order": "get_order", "runtime.run": "runtime_run", "class": "class_", "123": "x123", "-": "value"}
	if got := pythonMethods(tools); !maps.Equal(got, want) {
		t.Fatalf("methods %v, want %v", got, want)
	}
}

// A tool with a method of its own has a typed one, whatever its name, and the method calls that tool.
// Tools whose derived names collide are reachable through call_tool alone.
func TestPythonMethodsCallToolsNamedOtherwise(t *testing.T) {
	python := pythonWithMypy(t)
	var files map[string][]byte
	for _, c := range generationCases(t) {
		if c.name == "slot-python" {
			var err error
			if files, err = c.run(t); err != nil {
				t.Fatal(err)
			}
		}
	}
	root := t.TempDir()
	files["holon/client.py"] = []byte(pythonRuntime)
	files["main.py"] = []byte(`import asyncio
from holon.apps import Inputs, Outputs, apps, create_apps
async def typed() -> None:
    run: Outputs.schemas.runtime_run = await apps.schemas.tools.runtime_run(code="print(1)")
    stdout: str = run["stdout"]
    request: Inputs.schemas.runtime_run = {"code": stdout}
    await apps.schemas.tools.runtime_run(**request)
    await apps.schemas.tools.get_order(id="a")
    await apps.schemas.call_tool("get-order", id="a")
    await apps.schemas.call_tool("order.list")
    await apps.schemas.tools.class_(opt={"n": 1.0})
    await apps.schemas.tools.x123()
async def use() -> list[str]:
    create_apps({"orders": {"endpoint": "http://localhost"}})
    return [str(await getattr(apps.schemas.tools, method)()) for method in ("runtime_run", "get_order", "class_", "x123", "value", "moji")]
if __name__ == "__main__":
    print(" ".join(asyncio.run(use())))
`)
	files["wrong.py"] = []byte(`from holon.apps import apps
async def use() -> None:
    await apps.schemas.tools.runtime_run(code=1)
    await apps.schemas.tools.get_order2(id="a")
    await apps.schemas.tools.order_list()
    await apps.schemas.tools.order_list2()
`)
	writeFiles(t, root, files)
	run := func(args ...string) (string, error) {
		command := exec.CommandContext(t.Context(), python, args...)
		command.Dir = root
		output, err := command.CombinedOutput()
		return string(output), err
	}
	if output, err := run("-m", "mypy", "--strict", "--no-incremental", "main.py"); err != nil {
		t.Fatalf("mypy rejected the generated module or its use: %v\n%s", err, output)
	}
	output, err := run("-m", "mypy", "--no-incremental", "wrong.py")
	for _, want := range []string{`Argument "code" to "runtime_run"`, `has no attribute "get_order2"`, `has no attribute "order_list"`, `has no attribute "order_list2"`} {
		if err == nil || !strings.Contains(output, want) {
			t.Errorf("mypy did not report %s:\n%s", want, output)
		}
	}
	output, err = run("main.py")
	if want := "runtime.run get_order class 123 - émoji🎉\n"; err != nil || output != want {
		t.Fatalf("methods called %q, %v; want %q", output, err, want)
	}
}
