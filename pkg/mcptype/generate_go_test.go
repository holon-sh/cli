package mcptype

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGeneratedGoClientChecksInputFields(t *testing.T) {
	generated, err := generationCase{input: `{"target": "go", "identity": {"name": "caller", "version": "1"}, "go_module": "example.org/runtime", "contracts": {"orders": {"tools": [{"name": "lookup",
        "inputSchema": {"type": "object", "required": ["id", "rows", "byRegion"],
            "properties": {
                "id": {"type": "string"},
                "rows": {"type": "array", "items": {"$ref": "#/$defs/Row"}},
                "byRegion": {"type": "object", "additionalProperties": {"type": "object", "properties": {"quantity": {"type": "integer"}}}}
            },
            "$defs": {"Row": {"type": "object", "required": ["sku"], "properties": {"sku": {"type": "string"}}}}},
        "outputSchema": {"type": "object", "properties": {"total": {"type": "number"}}}}]}}}`}.run(t)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	writeFiles(t, filepath.Join(root, "holon"), generated)
	writeFiles(t, root, map[string][]byte{
		"go.mod":         []byte("module example.org/caller\n\ngo 1.27.0\n\nrequire example.org/runtime v0.0.0\n\nreplace example.org/runtime => ./runtime\n"),
		"runtime/go.mod": []byte("module example.org/runtime\n\ngo 1.27.0\n"),
		"runtime/stub.go": []byte(`package holon

import "context"

type ToolSchema struct{ Input, Output string }

type Dependency struct {
	Name  string
	Tools map[string]ToolSchema
}

func Call[Output any](context.Context, *Dependency, string, any) (Output, error) {
	var output Output
	return output, nil
}
`),
	})
	for _, test := range []struct{ name, body, wantError string }{
		{"declared fields", `
	input := apps.OrdersLookupInput{Id: "a"}
	input.Rows[0].Sku = "s"
	_ = input.ByRegion["eu"].Quantity
	_ = apps.OrdersLookupInputRow{Sku: "s"}
	output, _ := apps.Orders.Lookup(ctx, input)
	_ = output["undeclared"]`, ""},
		{"misspelled field", `
	_, _ = apps.Orders.Lookup(ctx, apps.OrdersLookupInput{Idd: "a"})`, "unknown field Idd"},
	} {
		t.Run(test.name, func(t *testing.T) {
			writeFiles(t, root, map[string][]byte{"main.go": []byte(`package main

import (
	"context"

	apps "example.org/caller/holon"
)

func main() {
	ctx := context.Background()` + test.body + "\n}\n")})
			command := exec.CommandContext(t.Context(), "go", "build", "./...")
			command.Dir = root
			command.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod", "GOPROXY=off")
			output, err := command.CombinedOutput()
			if (err != nil) != (test.wantError != "") || !strings.Contains(string(output), test.wantError) {
				t.Fatalf("go build = %v, want error %q\n%s", err, test.wantError, output)
			}
		})
	}
}
