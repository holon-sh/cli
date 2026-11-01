package typegen

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/holon-sh/cli/pkg/mcpcontract"
)

func TestDeclaredSources(t *testing.T) {
	const descriptor = `{"name":"example.com/checkout","version":"1.0.0","description":"Checkout"`
	const metadata = `,"_meta":{"io.modelcontextprotocol.registry/publisher-provided":{"sh.holon":`
	for _, test := range []struct {
		name string
		data string
		want []Source
		err  string
	}{
		{name: "no descriptor"},
		{name: "no requires", data: descriptor + `}`},
		{
			name: "sorted aliases and registry references",
			data: descriptor + metadata + `{"requires":{"orders":{"name":"example.com/orders","version":"1.0.0"},"docs":{"name":"example.com/documentation","version":"4.4.4","registry":"https://registry.example"}}}}}}`,
			want: []Source{
				{Alias: "docs", Registry: &Reference{Name: "example.com/documentation", Version: "4.4.4", Registry: "https://registry.example"}},
				{Alias: "orders", Registry: &Reference{Name: "example.com/orders", Version: "1.0.0"}},
			},
		},
		{name: "invalid descriptor", data: `{"name":"checkout","version":"1.0.0","description":"Checkout"}`, err: "namespace/name"},
		{name: "invalid alias", data: descriptor + metadata + `{"requires":{"Orders":{"name":"example.com/orders","version":"1.0.0"}}}}}}`, err: "invalid dependency name"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			if test.data != "" {
				put(t, root, "server.json", test.data)
			}
			got, err := DeclaredSources(t.Context(), root, Options{})
			if test.err != "" {
				if err == nil || !strings.Contains(err.Error(), filepath.Join(root, "server.json")) || !strings.Contains(err.Error(), test.err) {
					t.Fatalf("DeclaredSources error = %v, want server.json and %q", err, test.err)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(got, test.want) {
				t.Fatalf("DeclaredSources = %#v, %v; want %#v", got, err, test.want)
			}
		})
	}
}

// Without a server.json, a server's constructor names it and its publishing declaration supplies
// the rest of its descriptor; a server.json takes precedence.
func TestServerDescribesItself(t *testing.T) {
	const meta = `{"io.modelcontextprotocol.registry/publisher-provided":{"sh.holon":{"requires":{"orders":{"name":"example.com/orders","version":"1.0.0"}}}}}`
	for entry, source := range map[string]string{
		"server.py": "from mcp.server import MCPServer\nserver = MCPServer(\"example.com/checkout\", version=\"1.2.3\", description=\"Checkout\")\npublishing = {\"_meta\": " + meta + "}\n",
		"server.ts": "import { McpServer } from \"@modelcontextprotocol/server\";\nexport const publishing = { _meta: " + meta + " };\nexport default new McpServer({ name: \"example.com/checkout\", version: \"1.2.3\", description: \"Checkout\" });\n",
	} {
		t.Run(entry, func(t *testing.T) {
			if entry == "server.py" {
				needPython(t)
			} else {
				needNode(t)
			}
			root := t.TempDir()
			put(t, root, entry, source)
			sources, err := DeclaredSources(t.Context(), root, Options{})
			if err != nil || len(sources) != 1 || sources[0].Alias != "orders" || sources[0].Registry.Name != "example.com/orders" {
				t.Fatalf("DeclaredSources = %+v, %v", sources, err)
			}
			for _, want := range []mcpcontract.Identity{{Name: "example.com/checkout", Version: "1.2.3"}, {Name: "example.com/authored", Version: "2.0.0"}} {
				document, err := ResolveDocument(t.Context(), Source{Path: root}, Options{})
				if err != nil || document.Identity != want {
					t.Fatalf("identity = %+v, %v; want %+v", document, err, want)
				}
				put(t, root, "server.json", `{"name":"example.com/authored","version":"2.0.0","description":"Authored"}`)
			}
		})
	}
}
