package mcptype

import (
	"encoding/json"
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
	writeFiles(t, root, map[string][]byte{
		"holon/apps/__init__.py": files["__init__.py"],
		"holon/client.py":        []byte("import json\nclass Apps:\n    def __init__(self, identity, *rest):\n        print(json.dumps(identity))\ndef __getattr__(name):\n    return object\n"),
	})
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

// pythonRuntime stands in for holon.client: like the runtime, it names each tool's method after the
// tool. Its methods return the tool's name.
const pythonRuntime = `from __future__ import annotations
from types import SimpleNamespace
from typing import Any, Generic, TypeVar
T = TypeVar("T")
class Binding: ...
class CallOptions: ...
class CompleteResult(Generic[T]): ...
class ToolResult(Generic[T]): ...
def _method(tool: str) -> Any:
    async def call() -> str:
        return tool
    return call
class _App:
    def __init__(self, contract: dict[str, Any]) -> None:
        self.tools = SimpleNamespace(**{tool["name"]: _method(tool["name"]) for tool in contract["tools"]})
class Apps:
    def __init__(self, identity: object, contracts: dict[str, Any], bindings: Any = None) -> None:
        self._apps = {name: _App(contract) for name, contract in contracts.items() if bindings is None or name in bindings}
    def __getitem__(self, name: str) -> _App:
        return self._apps[name]
`

// A tool that is not named as an identifier has a method that is typed like any other and calls it,
// in a module that passes mypy.
func TestPythonMethodsCallToolsNamedOtherwise(t *testing.T) {
	python := "python3"
	if environment := os.Getenv("VIRTUAL_ENV"); environment != "" {
		python = filepath.Join(environment, "bin", "python")
	}
	if exec.CommandContext(t.Context(), python, "-c", "import mypy").Run() != nil {
		t.Skip("mypy is not installed")
	}
	var files map[string][]byte
	for _, c := range generationCases(t) {
		if c.name == "slot-python" {
			var err error
			if files, err = c.run(t); err != nil {
				t.Fatal(err)
			}
		}
	}
	files = map[string][]byte{"holon/apps/__init__.py": files["__init__.py"], "holon/client.py": []byte(pythonRuntime), "holon/ext/files.py": []byte("class File: ...\n"), "holon/ext/events.py": []byte("from typing import Generic, TypeVar\nD = TypeVar(\"D\")\nclass Subscription(Generic[D]): ...\n")}
	files["typed.py"] = []byte(`from holon.apps import Inputs, Outputs, apps
async def use(request: Inputs.schemas.runtime_run) -> Outputs.schemas.runtime_run:
    await apps.schemas.tools.runtime_run(code=1)
    return await apps.schemas.tools.runtime_run(**request)
`)
	root := t.TempDir()
	writeFiles(t, root, files)
	run := func(args ...string) string {
		command := exec.CommandContext(t.Context(), python, args...)
		command.Dir = root
		output, _ := command.CombinedOutput()
		return string(output)
	}
	output := run("-m", "mypy", "--strict", "typed.py")
	if !strings.Contains(output, `typed.py:3: error: Argument "code" to "runtime_run"`) || !strings.Contains(output, "Found 1 error") {
		t.Errorf("mypy reported something other than the wrong argument:\n%s", output)
	}
	// A client created without a binding that has renamed methods is left alone.
	output = run("-c", `import asyncio; from holon.apps import apps, create_apps; create_apps({"orders": {}}); print(asyncio.run(apps["schemas"].tools.runtime_run()))`)
	if output != "runtime.run\n" {
		t.Fatalf("runtime_run called %q, want runtime.run", output)
	}
}
