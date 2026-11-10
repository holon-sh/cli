package typegen

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/holon-sh/cli/pkg/mcpcontract"
	"github.com/holon-sh/protocol"
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
		{
			name: "sorted aliases, registry references, urls and project apps",
			data: descriptor + metadata + `{"requires":{"orders":{"name":"example.com/orders","version":"1.0.0"},"docs":{"name":"example.com/documentation","version":"4.4.4","registry":"https://registry.example"},"chat":{"url":"https://mcp.example/mcp"},"agent":{}}}}}}`,
			want: []Source{
				{Alias: "agent"},
				{Alias: "chat", Endpoint: "https://mcp.example/mcp"},
				{Alias: "docs", Registry: &protocol.RegistryRef{Name: "example.com/documentation", Version: "4.4.4", Registry: "https://registry.example"}},
				{Alias: "orders", Registry: &protocol.RegistryRef{Name: "example.com/orders", Version: "1.0.0"}},
			},
		},
		{name: "invalid descriptor", data: `{"name":"checkout","version":"1.0.0","description":"Checkout"}`, err: "namespace/name"},
		{name: "invalid alias", data: descriptor + metadata + `{"requires":{"Orders":{"name":"example.com/orders","version":"1.0.0"}}}}}}`, err: "dependency name \"Orders\""},
		{name: "unknown requires member", data: descriptor + metadata + `{"requires":{"docs":{"url":"https://mcp.example/mcp","header":[]}}}}}}`, err: `unknown object member name "header"`},
		{name: "a url with a version", data: descriptor + metadata + `{"requires":{"docs":{"url":"https://mcp.example/mcp","version":"1.0.0"}}}}}}`, err: "leave out name, version and registry"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			if test.data != "" {
				put(t, root, "server.json", test.data)
			}
			got, err := DeclaredSources(root, nil)
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

func TestDeclaredURLOverride(t *testing.T) {
	live := tokenServer(t, "secret")
	root := t.TempDir()
	put(t, root, "server.json", `{"name":"example.com/checkout","version":"1.0.0","description":"Checkout","_meta":{"io.modelcontextprotocol.registry/publisher-provided":{"sh.holon":{"requires":{"docs":{},"orders":{"name":"example.com/orders","version":"1.0.0"}}}}}}`)
	for _, test := range []struct{ name, location, want string }{
		{name: "set", location: live.URL},
		{name: "invalid", location: "ftp://docs.example", want: "DOCS_MCP_URL: MCP URL must be an HTTP(S) URL"},
		{name: "unset", want: "dependency docs names no server"},
	} {
		t.Run(test.name, func(t *testing.T) {
			sources, err := DeclaredSources(root, []string{"DOCS_MCP_URL=" + test.location, "ORDERS_MCP_URL=" + live.URL})
			if err != nil {
				t.Fatal(err)
			}
			if sources[1].Registry == nil || sources[1].Endpoint != "" {
				t.Fatalf("registry entry = %+v, want it left alone", sources[1])
			}
			var progress strings.Builder
			document, err := ResolveDocument(t.Context(), sources[0], Options{Stderr: &progress, Env: []string{"DOCS_MCP_TOKEN=secret"}})
			if test.want != "" {
				if err == nil || !strings.Contains(err.Error(), test.want) {
					t.Fatalf("ResolveDocument = %v, want %q", err, test.want)
				}
				return
			}
			if err != nil || document.Identity.Name != "orders" || !strings.Contains(progress.String(), live.URL) {
				t.Fatalf("ResolveDocument = %+v, %v; progress %q", document, err, progress.String())
			}
		})
	}
}

func TestServerDescribesItself(t *testing.T) {
	for entry, source := range map[string]string{
		"server.py": "from mcp.server import MCPServer\nserver = MCPServer(\"example.com/checkout\", version=\"1.2.3\", description=\"Checkout\")\n",
		"server.ts": "import { McpServer } from \"@modelcontextprotocol/server\";\nexport default new McpServer({ name: \"example.com/checkout\", version: \"1.2.3\", description: \"Checkout\" });\n",
	} {
		t.Run(entry, func(t *testing.T) {
			if entry == "server.py" {
				needPython(t)
			} else {
				needNode(t)
			}
			root := t.TempDir()
			put(t, root, entry, source)
			want := mcpcontract.Identity{Name: "example.com/checkout", Version: "1.2.3"}
			if document, err := ResolveDocument(t.Context(), Source{Path: root}, Options{}); err != nil || document.Identity != want {
				t.Fatalf("identity = %+v, %v; want %+v", document, err, want)
			}
		})
	}
}

func TestOwnServer(t *testing.T) {
	const node = `import { McpServer } from "@modelcontextprotocol/server";`
	for _, test := range []struct {
		name  string
		files map[string]string
		want  string // the directory relative to the project, when one is found
		fails bool
	}{
		{name: "none", files: map[string]string{"index.ts": node}},
		{name: "entry at the root", files: map[string]string{"server.py": pythonProvider}, want: "."},
		{name: "entry in a server directory", files: map[string]string{"server/server.ts": node}, want: "server"},
		{name: "a web server beside an MCP server and a page", files: map[string]string{"server.js": "export default {};", "src/page.ts": node, "server/server.ts": node}, want: "server"},
		{name: "a compiled binary named server", files: map[string]string{"server": ""}},
		{name: "two servers", files: map[string]string{"server.py": pythonProvider, "server/server.py": pythonProvider}, fails: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, content := range test.files {
				put(t, dir, name, content)
			}
			own, err := OwnServer(dir)
			if (err != nil) != test.fails {
				t.Fatalf("OwnServer error = %v, want failure %t", err, test.fails)
			}
			var got string
			if own != nil {
				got, _ = filepath.Rel(dir, own.Path)
			}
			if got != test.want {
				t.Fatalf("OwnServer = %q, want %q", got, test.want)
			}
		})
	}
}
