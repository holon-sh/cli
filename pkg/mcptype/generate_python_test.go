package mcptype

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"github.com/holon-sh/cli/pkg/mcpcontract"
)

func TestPythonPayloadPreservesStringLiterals(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not installed")
	}
	identity := mcpcontract.Identity{Name: "a'b\"\\\n\r\t\x00\x1f\x7fé😀\u2028", Version: "1"}
	files, err := Generate(t.Context(), GenerateRequest{Target: "python", Identity: identity})
	if err != nil {
		t.Fatal(err)
	}
	// Evaluate the generated payload without importing the separate Python runtime.
	_, payload, found := strings.Cut(string(files["holon/apps/__init__.py"]), "_identity, _contracts = ")
	if !found {
		t.Fatal("generated module has no payload")
	}
	assignment, _, _ := strings.Cut(payload, "\n")
	program := "import json as _json\n_identity, _contracts = " + assignment + "\nprint(_json.dumps(_identity))"
	output, err := exec.CommandContext(t.Context(), python, "-c", program).CombinedOutput()
	if err != nil {
		t.Fatalf("invalid Python payload: %v\n%s", err, output)
	}
	var decoded mcpcontract.Identity
	if err := json.Unmarshal(output, &decoded); err != nil || decoded != identity {
		t.Fatalf("Python payload changed the identity: %+v, %v", decoded, err)
	}
}
