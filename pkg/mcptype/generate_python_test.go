package mcptype

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
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
	for name, content := range files {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
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
