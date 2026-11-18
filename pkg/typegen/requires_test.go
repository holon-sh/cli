package typegen

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/holon-sh/cli/internal/testfiles"
	"github.com/holon-sh/cli/pkg/mcpcontract"
	"github.com/holon-sh/cli/pkg/mcpregistry"
)

func TestRequiredSources(t *testing.T) {
	const descriptor = `{"name":"example.com/checkout","version":"1.0.0","description":"Checkout"`
	const metadata = `,"_meta":{"io.modelcontextprotocol.registry/publisher-provided":{"sh.holon":`
	for _, test := range []struct {
		name string
		data string
		want map[string]Source // nil when the descriptor is rejected
	}{
		{
			name: "registry references and urls",
			data: descriptor + metadata + `{"requires":{"orders":{"name":"example.com/orders","version":"1.0.0"},"docs":{"name":"example.com/documentation","version":"4.4.4","registry":"https://registry.example"},"chat":{"url":"https://mcp.example/mcp"}}}}}}`,
			want: map[string]Source{
				"chat":   {Alias: "chat", Server: Endpoint("https://mcp.example/mcp")},
				"docs":   {Alias: "docs", Server: Registry{Name: "example.com/documentation", Version: "4.4.4", Registry: "https://registry.example"}},
				"orders": {Alias: "orders", Server: Registry{Name: "example.com/orders", Version: "1.0.0"}},
			},
		},
		{name: "invalid descriptor", data: `{"name":"checkout","version":"1.0.0","description":"Checkout"}`},
		{name: "invalid alias", data: descriptor + metadata + `{"requires":{"Orders":{"name":"example.com/orders","version":"1.0.0"}}}}}}`},
		{name: "unknown requires member", data: descriptor + metadata + `{"requires":{"docs":{"url":"https://mcp.example/mcp","header":[]}}}}}}`},
		{name: "a url with a version", data: descriptor + metadata + `{"requires":{"docs":{"url":"https://mcp.example/mcp","version":"1.0.0"}}}}}}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			put(t, root, "server.json", test.data)
			requires, err := Requires(root)
			if test.want == nil {
				if err == nil {
					t.Fatalf("Requires accepted %s", test.data)
				}
				return
			}
			got := map[string]Source{}
			for alias, requirement := range requires {
				if got[alias], err = RequiredSource(alias, requirement, Path{}); err != nil {
					t.Fatal(err)
				}
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("required sources = %#v, %v; want %#v", got, err, test.want)
			}
		})
	}
	if _, err := RequiredSource("docs", mcpregistry.Requirement{}, Path{}); err == nil {
		t.Fatal("RequiredSource accepted a requirement with no server")
	}
}

// The real runners report a server's description and its lists, here for a plain McpServer export,
// which serves each request in turn.
func TestServerDescribesItself(t *testing.T) {
	for entry, source := range map[string]string{
		"server.py": "from mcp.server import MCPServer\nserver = MCPServer(\"example.com/checkout\", version=\"1.2.3\", description=\"Checkout\")\n@server.tool()\ndef read(id: str) -> str:\n    return id\n",
		"server.ts": "import { McpServer } from \"@modelcontextprotocol/server\";\nconst server = new McpServer({ name: \"example.com/checkout\", version: \"1.2.3\", description: \"Checkout\" });\nserver.registerTool(\"read\", {}, () => ({ content: [] }));\nexport default server;\n",
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
			document, err := ResolveDocument(t.Context(), Source{Server: Path{Dir: root}}, Options{})
			if err != nil || document.Identity != want || len(document.Schema.Tools) != 1 || document.Schema.Tools[0].Name != "read" {
				t.Fatalf("document = %+v, %v; want %+v with the tool read", document, err, want)
			}
		})
	}
}

func TestAppConsumerFindsTheOwnServer(t *testing.T) {
	const node = `import { McpServer } from "@modelcontextprotocol/server";`
	for _, test := range []struct {
		name  string
		files map[string]string
		want  string // the directory relative to the project, when one is found
		fails bool
	}{
		{name: "entry at the root", files: map[string]string{"server.py": pythonProvider}, want: "."},
		{name: "a web server beside an MCP server and a page", files: map[string]string{"server.js": "export default {};", "src/page.ts": node, "server/server.ts": node}, want: "server"},
		{name: "a compiled binary named server", files: map[string]string{"server": ""}},
		{name: "two servers", files: map[string]string{"server.py": pythonProvider, "server/server.py": pythonProvider}, fails: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			testfiles.Write(t, dir, test.files)
			c, err := AppConsumer("app", dir)
			if (err != nil) != test.fails {
				t.Fatalf("AppConsumer error = %v, want failure %t", err, test.fails)
			}
			var got string
			if c != nil {
				got, _ = filepath.Rel(dir, c.Server.Dir)
			}
			if got != test.want {
				t.Fatalf("own server = %q, want %q", got, test.want)
			}
		})
	}
}
