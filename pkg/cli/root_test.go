package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/holon-sh/cli/pkg/mcpcontract"
	"github.com/holon-sh/cli/pkg/typegen"
)

// run returns stdout on success or the expected error's text on failure.
func run(t *testing.T, wantErr string, args ...string) string {
	t.Helper()
	var output bytes.Buffer
	command := NewCommand("test", func(context.Context, string) (string, error) { return "signed-in", nil })
	command.SetArgs(args)
	command.SetOut(&output)
	command.SetErr(new(bytes.Buffer))
	err := command.Execute()
	if wantErr != "" {
		if err == nil || !strings.Contains(err.Error(), wantErr) {
			t.Fatalf("%v = %v, want %q", args, err, wantErr)
		}
		return err.Error()
	}
	if err != nil {
		t.Fatalf("%v: %v", args, err)
	}
	return output.String()
}

func writeCLIFile(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func installCLIRuntime(t *testing.T, root string) {
	t.Helper()
	for _, pkg := range []string{"holon-sh", "holon-ui"} {
		writeCLIFile(t, root, "node_modules/"+pkg+"/package.json", `{"name":"`+pkg+`","holon":{"contractVersion":2}}`)
	}
}

func TestConsumerSelectsTheOwnServer(t *testing.T) {
	for _, test := range []struct {
		name          string
		servers       []string
		server, alias string // the own server's directory, relative to the project
		sources       []string
		err           string
	}{
		{name: "an app finds its server and the requires beside it", server: "server", sources: []string{"orders"}},
		{name: "an alias stays on the own server", servers: []string{"shop=./server"}, server: "server", alias: "shop"},
		{name: "-s replaces the declared requires", servers: []string{"docs=https://mcp.example/mcp"}, server: "server", sources: []string{"docs"}},
		{name: "one own server", servers: []string{"./server", "./server/server.py"}, err: "one own server"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Chdir(root)
			writeCLIFile(t, root, "package.json", `{"dependencies":{"holon-sh":"^0.0.1"}}`)
			writeCLIFile(t, root, "server/server.py", pythonServer)
			writeCLIFile(t, root, "server/server.json", `{"name":"example.com/view","version":"1.0.0","description":"View"`+requiresOrders)
			c, err := consumer(root, test.servers)
			if test.err != "" {
				if err == nil || !strings.Contains(err.Error(), test.err) {
					t.Fatalf("consumer = %v, want %q", err, test.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var server, alias string
			if c.Server != nil {
				server, _ = filepath.Rel(root, c.Server.Path)
				alias = c.Server.Alias
			}
			var sources []string
			for _, source := range c.Sources {
				sources = append(sources, source.Alias)
			}
			if server != test.server || alias != test.alias || !slices.Equal(sources, test.sources) {
				t.Fatalf("consumer = %q %q %v, want %q %q %v", server, alias, sources, test.server, test.alias, test.sources)
			}
		})
	}
}

func TestProjectRunsForEachApp(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	writeCLIFile(t, root, "holon.yaml", `apps:
  - {name: view, path: view, access: {server: {}}}
  - {name: orders, path: orders, access: {server: {}}}
  - {name: docs, remote: {url: "https://docs.example/mcp"}, access: {server: {}}}
  - {name: site, path: site, access: {server: {}}}
  - {name: search, registry: {name: example.com/search, version: "2.0.0"}, access: {server: {}}}
`)
	view := filepath.Join(root, "view")
	writeCLIFile(t, view, "package.json", `{"dependencies":{"holon-sh":"^0.0.1"}}`)
	installCLIRuntime(t, view)
	requires := `"docs":{},"orders":{},"search":{"name":"example.com/search","version":"1.0.0"}`
	writeCLIFile(t, view, "server.json", `{"name":"example.com/view","version":"1.0.0","description":"View","_meta":{"io.modelcontextprotocol.registry/publisher-provided":{"sh.holon":{"requires":{`+requires+`}}}}}`)
	writeCLIFile(t, root, "orders/server.py", pythonServer)
	writeCLIFile(t, root, "orders/server.json", `{"name":"example.com/orders","version":"1.0.0","description":"Orders"}`)
	writeCLIFile(t, root, "site/index.html", "<p>no runtime and no MCP server</p>")

	// The apps holon dev builds and checks: a client, and a server that makes no calls; not the site.
	// A dependency is the file's app of that name, even where server.json pins another version.
	consumers, err := projectConsumers(root)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, c := range consumers {
		names = append(names, c.Name)
	}
	if !slices.Equal(names, []string{"view", "orders"}) || consumers[1].Server == nil {
		t.Fatalf("consumers = %+v", consumers)
	}
	sources := consumers[0].Sources
	if len(sources) != 3 || sources[0].Endpoint != "https://docs.example/mcp" || sources[1].Path != filepath.Join(root, "orders") || sources[2].Registry.Version != "2.0.0" {
		t.Fatalf("view sources = %+v", sources)
	}

	// check reaches the app's clients, not the project root, which declares no runtime of its own.
	run(t, filepath.Join(view, "generated/holon-sh")+" has no generated clients", "check", "--no-generate")
	run(t, "-s names one app's dependencies; pass that app's DIR", "check", "-s", "x")
}

func TestUsageErrors(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{"check", "a", "b"}, "one DIR at most; dependencies are -s values"},
		{[]string{"check", "--no-generate", "-s", "x"}, "-s has no effect"},
		{[]string{"check", "missing-dir"}, "is not a directory"},
		{[]string{"types", "generate", "--contract", "a", "-s", "b"}, "takes one SERVER and nothing else"},
		{[]string{"types", "generate", "-o", "contract.json"}, "--output writes a --contract document"},
	} {
		run(t, test.want, test.args...)
	}
	writeCLIFile(t, root, "package.json", `{"dependencies":{"holon-sh":"^0.0.1"}}`)
	run(t, "holon-sh is not installed", "check", "--no-generate")
	installCLIRuntime(t, root)
	writeCLIFile(t, root, "server.json", `{"name":"example.com/checkout","version":"1.0.0","description":"Checkout"`+requiresOrders)
	run(t, "example.com/orders@1.0.0", "types", "generate")
}

// requiresOrders ends a server.json that requires an orders server from an unreachable registry.
const requiresOrders = `,"_meta":{"io.modelcontextprotocol.registry/publisher-provided":{"sh.holon":{"requires":{"orders":{"name":"example.com/orders","version":"1.0.0","registry":"https://127.0.0.1:1"}}}}}}`

const pythonServer = "from mcp.server import MCPServer\nserver = MCPServer('provider')\n@server.tool()\ndef read(id: str) -> str:\n    return id\n"

func TestInitRefusals(t *testing.T) {
	for _, test := range []struct {
		args []string
		err  string
	}{
		{[]string{"orders"}, "--lang is required"},
		{[]string{"orders", "--lang", "rb"}, "use go, py or ts"},
		{[]string{"_", "--lang", "py"}, "cannot name an app"},
		{[]string{"orders", "--lang", "py", "--name", "Order Desk"}, "is not an MCP server name"},
		{[]string{"orders", "--lang", "py", "--name", "io.github.acme/" + strings.Repeat("a", 200)}, "is not an MCP server name"},
	} {
		t.Run(strings.Join(test.args, " "), func(t *testing.T) {
			t.Chdir(t.TempDir())
			cmd := NewInitCommand()
			cmd.SetIn(strings.NewReader(""))
			cmd.SetOut(new(bytes.Buffer))
			cmd.SetErr(new(bytes.Buffer))
			cmd.SetArgs(test.args)
			if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), test.err) {
				t.Fatalf("init = %v, want %q", err, test.err)
			}
			if files, err := os.ReadDir("."); err != nil || len(files) != 0 {
				t.Fatalf("init wrote files: %v, %v", files, err)
			}
		})
	}
}

func TestInitManifest(t *testing.T) {
	for _, test := range []struct{ manifest, dir, file, path string }{
		{"holon.yaml", "orders", "holon.yaml", "orders"},
		{"", "orders", "orders/holon.yaml", "."},
		{"holon.yml", ".", "holon.yml", "."},
		{"holon.yaml", "../outside", "../outside/holon.yaml", "."},
	} {
		t.Run(test.manifest+"/"+test.dir, func(t *testing.T) {
			t.Chdir(t.TempDir())
			working, err := os.Getwd()
			if err != nil {
				t.Fatal(err)
			}
			if test.manifest != "" {
				writeCLIFile(t, working, test.manifest, "apps: []\n")
			}
			file, path, err := manifestFor(filepath.Join(working, test.dir))
			if err != nil || file != filepath.Join(working, test.file) || path != test.path {
				t.Fatalf("manifest = %s, %s, %v; want %s, %s", file, path, err, test.file, test.path)
			}
		})
	}
}

func TestContractOfAPathSource(t *testing.T) {
	dir := t.TempDir()
	writeCLIFile(t, dir, "server.json", `{"name":"example.com/provider","version":"1.0.0","description":"Provider"}`)
	writeCLIFile(t, dir, "server.py", pythonServer)
	output := run(t, "", "types", "generate", "--contract", filepath.Join(dir, "server.py"))
	var metadata mcpcontract.Metadata
	if err := json.Unmarshal([]byte(output), &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata.Identity.Name != "example.com/provider" || metadata.Schema == nil || len(metadata.Schema.Tools) != 1 || metadata.Schema.Tools[0].Name != "read" {
		t.Fatalf("provider contract = %s", output)
	}
	// -o commits the same document beside server.json, where it is checked against the server.
	committed := filepath.Join(dir, typegen.ContractFile)
	if printed := run(t, "", "types", "generate", "--contract", dir, "-o", committed); printed != "" {
		t.Fatalf("-o also printed %s", printed)
	}
	if data, err := os.ReadFile(committed); err != nil || string(data) != output {
		t.Fatalf("committed contract = %s, %v", data, err)
	}
}
