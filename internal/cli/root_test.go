package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestContractFlagsRequireAnExplicitRemoteIdentity(t *testing.T) {
	for _, args := range [][]string{{"types", "contract", "--url", "https://example.com/mcp"}, {"types", "contract", "app", "--url", "https://example.com/mcp", "--name", "example.com/app", "--version", "1"}, {"types", "contract", "--name", "example.com/app"}} {
		command := NewCommand("test")
		command.SetArgs(args)
		command.SetOut(new(bytes.Buffer))
		command.SetErr(new(bytes.Buffer))
		if err := command.Execute(); err == nil {
			t.Fatalf("accepted conflicting/missing flags: %v", args)
		}
	}
}
func TestStandaloneVersionAndRemovedEmitFlag(t *testing.T) {
	var output bytes.Buffer
	command := NewCommand("0.0.1")
	command.SetOut(&output)
	command.SetArgs([]string{"--version"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "standalone types CLI") {
		t.Fatalf("ambiguous version: %s", &output)
	}
	command = NewCommand("test")
	command.SetArgs([]string{"check", "--emit"})
	command.SetErr(new(bytes.Buffer))
	if err := command.Execute(); err == nil || !strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("legacy emit flag accepted: %v", err)
	}
}

func TestTypesCompileWritesAGenerateRequestIntoTheOutputDirectory(t *testing.T) {
	root := t.TempDir()
	request := `{"target":"python","identity":{"name":"caller","version":"1"},"requires":{"orders":{"name":"orders","version":"1"}},"contracts":{"orders":{"tools":[{"name":"read","inputSchema":{"type":"object","properties":{"id":{"type":"string"}},"required":["id"]}}]}},"python_module":"holon/apps/__init__.py","python_runtime_module":"holon.client","banner":"holon-generated-apps-v1 app=caller"}`
	if err := os.WriteFile(filepath.Join(root, "request.json"), []byte(request), 0o644); err != nil {
		t.Fatal(err)
	}
	command := NewCommand("test")
	command.SetOut(new(bytes.Buffer))
	command.SetErr(new(bytes.Buffer))
	command.SetArgs([]string{"types", "compile", filepath.Join(root, "request.json"), "--out", filepath.Join(root, "out")})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	module, err := os.ReadFile(filepath.Join(root, "out", "holon", "apps", "__init__.py"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(module), "# holon-generated-apps-v1 app=caller") || !strings.Contains(string(module), "async def read(") {
		t.Fatalf("unexpected module:\n%s", module)
	}
	command = NewCommand("test")
	command.SetErr(new(bytes.Buffer))
	command.SetArgs([]string{"types", "compile", "--out", root})
	command.SetIn(strings.NewReader(`{"target":"python","identity":{"name":"x","version":"1"},"unknown":1}`))
	if err := command.Execute(); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("unknown request fields accepted: %v", err)
	}
}
