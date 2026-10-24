package typegen

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
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
	for _, test := range []struct {
		name                  string
		apps                  []string
		foreignDependencySlot bool
	}{
		{name: "caller only", apps: []string{"caller"}},
		{name: "caller with foreign dependency slot", apps: []string{"caller"}, foreignDependencySlot: true},
		{name: "both explicitly", apps: []string{"caller", "provider"}},
		{name: "all by default"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			caller, provider := filepath.Join(root, "caller"), filepath.Join(root, "provider")
			writeProvider(t, caller, "caller", "from holon.apps import apps\n"+pythonProvider, map[string]Reference{
				"provider": {Name: "example.com/provider", Version: "1.0.0"},
			})
			writeProvider(t, provider, "provider", pythonProvider, nil)
			// Missing runtimes must only be installed for selected apps.
			put(t, caller, "pyproject.toml", "[project]\nname = 'caller'\n")
			put(t, provider, "pyproject.toml", "[project]\nname = 'provider'\n")
			old := "# " + mcpcontract.Marker + " app=provider\n# previous generated content\n"
			if test.foreignDependencySlot {
				old = "# user-owned module\n"
			}
			put(t, provider, "holon/apps/__init__.py", old)
			manifest := Manifest{Apps: []App{{Name: "caller", Path: caller}, {Name: "provider", Path: provider, Access: json.RawMessage(`{"server":{}}`)}}}
			var output bytes.Buffer
			err := Generate(t.Context(), manifest, Options{Apps: test.apps, NoInstall: true, Stdout: &output, Stderr: &output})
			if err != nil {
				t.Fatalf("generate: %v\n%s", err, &output)
			}
			if module := read(t, caller, "holon/apps/__init__.py"); !strings.Contains(module, "async def read(") {
				t.Fatalf("caller did not get the extracted provider contract:\n%s", module)
			}
			selected := len(test.apps) == 0 || slices.Contains(test.apps, "provider")
			if changed := read(t, provider, "holon/apps/__init__.py") != old; changed != selected {
				t.Fatalf("dependency slot changed = %t, selected = %t", changed, selected)
			}
			for _, diagnostic := range []string{"provider: uv add", "provider: generated"} {
				if strings.Contains(output.String(), diagnostic) != selected {
					t.Fatalf("dependency install/generation selection incorrect:\n%s", &output)
				}
			}
		})
	}
}
