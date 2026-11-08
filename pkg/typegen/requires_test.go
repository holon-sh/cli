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
		{
			name: "a url beside a record",
			data: descriptor + metadata + `{"requires":{"docs":{"url":"https://mcp.example/mcp"},"orders":{"name":"example.com/orders","version":"1.0.0"}}}}}}`,
			want: []Source{{Alias: "docs", Endpoint: "https://mcp.example/mcp"}, {Alias: "orders", Registry: &Reference{Name: "example.com/orders", Version: "1.0.0"}}},
		},
		{name: "invalid descriptor", data: `{"name":"checkout","version":"1.0.0","description":"Checkout"}`, err: "namespace/name"},
		{name: "invalid alias", data: descriptor + metadata + `{"requires":{"Orders":{"name":"example.com/orders","version":"1.0.0"}}}}}}`, err: "invalid dependency name"},
		{name: "a url with a version", data: descriptor + metadata + `{"requires":{"docs":{"url":"https://mcp.example/mcp","version":"1.0.0"}}}}}}`, err: "leave out name, version and registry"},
		{name: "a url that is not an endpoint", data: descriptor + metadata + `{"requires":{"docs":{"url":"ftp://mcp.example/mcp"}}}}}}`, err: "HTTP(S) URL"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			if test.data != "" {
				put(t, root, "server.json", test.data)
			}
			got, err := DeclaredSources(root)
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

// A running server with no registry record is declared by its url, which holon types check generates
// from when no -s names it.
func TestADeclaredEndpointIsGeneratedAndChecked(t *testing.T) {
	live := liveServer(t, "docs")
	defer live.Close()
	app := typeScriptApp(t)
	put(t, app, "server.json", `{"name":"example.com/app","version":"1.0.0","description":"App","_meta":{"io.modelcontextprotocol.registry/publisher-provided":{"sh.holon":{"requires":{"docs":{"url":"`+live.URL+`"}}}}}}`)
	put(t, app, "src/index.ts", "import type { Inputs } from \"holon-sh\";\nexport const input: Inputs[\"docs\"][\"read\"] = { id: \"a\" };\n")
	sources, err := DeclaredSources(app)
	if err != nil {
		t.Fatal(err)
	}
	if err := Generate(t.Context(), []Consumer{{Name: "app", Dir: app, Targets: []string{TargetTS}, Sources: sources}}, Options{}); err != nil {
		t.Fatal(err)
	}
	if output, err := check(t, app); err != nil {
		t.Fatalf("check: %v\n%s", err, output)
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
