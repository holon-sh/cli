package mcptype

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A stand-in for the runtime module, which generated clients need only to compile.
const goRuntimeStub = `package holon

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
`

const goClientContract = `{"tools": [
    {"name": "lookup",
     "inputSchema": {"type": "object", "required": ["id", "rows", "byRegion"],
        "properties": {
            "id": {"type": "string"},
            "filter": {"type": "object", "required": ["status"], "properties": {"status": {"type": "string"}}},
            "rows": {"type": "array", "items": {"$ref": "#/$defs/Row"}},
            "byRegion": {"type": "object", "additionalProperties": {"type": "object", "properties": {"quantity": {"type": "integer"}}}}
        },
        "$defs": {"Row": {"type": "object", "required": ["sku"], "properties": {"sku": {"type": "string"}}}}},
     "outputSchema": {"type": "object", "properties": {"total": {"type": "number"}}}},
    {"name": "annotate", "inputSchema": {"type": "object", "properties": {"id": {"type": "string"}}, "additionalProperties": true}},
    {"name": "ping", "inputSchema": {"type": "object"}}
]}`

func TestGeneratedGoInputsFollowTheirSchema(t *testing.T) {
	request := generationCase{input: `{"target": "go", "identity": {"name": "caller", "version": "1"}, "go_module": "example.org/runtime",
        "requires": {"orders": {"name": "orders", "version": "1"}}, "contracts": {"orders": ` + goClientContract + `}}`}
	generated, err := request.run(t)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	files := map[string]string{
		"go.mod":          "module example.org/caller\n\ngo 1.27.0\n\nrequire example.org/runtime v0.0.0\n\nreplace example.org/runtime => ./runtime\n",
		"runtime/go.mod":  "module example.org/runtime\n\ngo 1.27.0\n",
		"runtime/stub.go": goRuntimeStub,
	}
	for name, content := range generated {
		files[filepath.Join("holon", name)] = string(content)
	}
	for name, content := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	build := func(t *testing.T, body string) (string, error) {
		t.Helper()
		source := "package main\n\nimport (\n\t\"context\"\n\n\tapps \"example.org/caller/holon\"\n)\n\nfunc main() {\n\tctx := context.Background()\n\tvar input apps.OrdersLookupInput\n" + body + "\n\t_, _ = apps.Orders.Lookup(ctx, input)\n}\n"
		if err := os.WriteFile(filepath.Join(root, "main.go"), []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
		command := exec.CommandContext(t.Context(), "go", "build", "./...")
		command.Dir = root
		command.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod", "GOPROXY=off")
		output, err := command.CombinedOutput()
		return string(output), err
	}

	t.Run("declared fields compile and open schemas stay maps", func(t *testing.T) {
		output, err := build(t, `
	input.Id = "a"
	input.Filter.Status = "open"
	input.Rows[0].Sku = "s"
	_ = input.ByRegion["eu"].Quantity
	_ = apps.OrdersLookupInputRow{Sku: "s"}
	result, _ := apps.Orders.Lookup(ctx, apps.OrdersLookupInput{Id: "a"})
	_ = result["undeclared"]
	_, _ = apps.Orders.Annotate(ctx, apps.OrdersAnnotateInput{"undeclared": nil})
	_, _ = apps.Orders.Ping(ctx, apps.OrdersPingInput{"undeclared": nil})`)
		if err != nil {
			t.Fatalf("valid client failed to compile: %v\n%s", err, output)
		}
	})

	t.Run("misspelled fields do not compile", func(t *testing.T) {
		output, err := build(t, `
	_ = apps.OrdersLookupInput{Idd: "a"}
	input.Filter.Statuss = "open"
	input.Rows[0].Skuu = "s"
	_ = input.ByRegion["eu"].Quantityy
	_ = apps.OrdersLookupInputRow{Skuu: "s"}`)
		if err == nil {
			t.Fatal("misspelled fields compiled")
		}
		for _, misspelled := range []string{"Idd", "Statuss", "Skuu", "Quantityy"} {
			if !strings.Contains(output, misspelled) {
				t.Errorf("the compiler did not reject %s:\n%s", misspelled, output)
			}
		}
	})
}
