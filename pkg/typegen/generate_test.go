package typegen

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/holon-sh/cli/pkg/mcpcontract"
)

const pythonProvider = "from mcp.server import MCPServer\nserver = MCPServer(\"provider\")\n@server.tool()\ndef read(id: str) -> str:\n    return id\n"

func writeProvider(t *testing.T, root, name, source string, requires map[string]Reference) {
	t.Helper()
	put(t, root, "server.py", source)
	put(t, root, "server.json", string(mustJSON(t, map[string]any{
		"name": "example.com/" + name, "version": "1.0.0", "description": name,
		"_meta": map[string]any{PublisherMetadata: map[string]any{"sh.holon": map[string]any{"requires": requires}}},
	})))
}

func TestGenerateOnlyWritesSelectedApps(t *testing.T) {
	if _, err := typeLanguages["python"].localProgram(context.Background()); err != nil {
		t.Skip(err)
	}
	root := t.TempDir()
	caller, provider := filepath.Join(root, "caller"), filepath.Join(root, "provider")
	inventory := filepath.Join(root, "inventory")
	writeProvider(t, caller, "caller", "from holon.apps import apps\n"+pythonProvider+"unused = apps.inventory\n", map[string]Reference{
		"provider": {Name: "example.com/provider", Version: "1.0.0"},
	})
	writeProvider(t, provider, "provider", pythonProvider, nil)
	writeProvider(t, inventory, "inventory", pythonProvider, nil)
	put(t, caller, "pyproject.toml", "[project]\nname = 'caller'\n")
	put(t, provider, "pyproject.toml", "[project]\nname = 'provider'\n")
	old := "# " + mcpcontract.Marker + " app=provider\n# previous generated content\n"
	put(t, provider, "holon/apps/__init__.py", old)
	manifest := Manifest{Apps: []App{
		{Name: "caller", Path: caller},
		{Name: "provider", Path: provider, Access: json.RawMessage(`{"server":{}}`)},
		{Name: "inventory", Path: inventory, Access: json.RawMessage(`{"server":{}}`)},
	}}
	var output bytes.Buffer
	if err := Generate(t.Context(), manifest, Options{Apps: []string{"caller"}, NoInstall: true, Stdout: &output, Stderr: &output}); err != nil {
		t.Fatalf("generate: %v\n%s", err, &output)
	}
	if module := read(t, caller, "holon/apps/__init__.py"); !strings.Contains(module, "async def read(") || strings.Contains(module, "inventory") {
		t.Fatalf("caller must get only its explicitly required provider's client:\n%s", module)
	}
	if read(t, provider, "holon/apps/__init__.py") != old {
		t.Fatal("generation changed the dependency's slot")
	}
	if strings.Contains(output.String(), "provider: uv add") {
		t.Fatalf("generation installed a dependency's runtime:\n%s", &output)
	}
}
