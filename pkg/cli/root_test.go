package cli

import (
	"bytes"
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
	command := NewCommand("test")
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

func installCLISlot(t *testing.T, root string) {
	t.Helper()
	for _, pkg := range []string{"holon-sh", "holon-ui"} {
		writeCLIFile(t, root, "node_modules/"+pkg+"/package.json", `{"name":"`+pkg+`","exports":{".":{"types":"./generated/index.d.ts"}},"holon":{"slot":"generated","contractVersion":1}}`)
		writeCLIFile(t, root, "node_modules/"+pkg+"/generated/holon-generated.json", `{"marker":"`+mcpcontract.Marker+`","placeholder":true}`)
	}
}

func TestOwnServerIsABrowserClient(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	writeCLIFile(t, root, "package.json", `{"dependencies":{"holon-sh":"^0.0.1","holon-ui":"^0.0.1"}}`)
	writeCLIFile(t, root, "server.py", pythonServer)
	writeCLIFile(t, root, "server.json", `{"name":"example.com/view","version":"1.0.0","description":"View"}`)
	installCLISlot(t, root)
	orders := t.TempDir()
	writeCLIFile(t, orders, "server.py", pythonServer)
	writeCLIFile(t, orders, "server.json", `{"name":"example.com/orders","version":"1.0.0","description":"Orders"}`)
	run(t, "", "types", "generate", "-s", "./server.py", "-s", orders)
	// The own server is callable from the browser but is not a dependency of itself.
	for pkg, want := range map[string]bool{"holon-sh": false, "holon-ui": true} {
		data, err := os.ReadFile(filepath.Join(root, "node_modules", pkg, "generated/index.d.ts"))
		if err != nil || strings.Contains(string(data), `"view"`) != want || !strings.Contains(string(data), `"orders"`) {
			t.Fatalf("%s = %s, %v", pkg, data, err)
		}
	}
	// Without -s the project finds the same server, and the explicit dependency is gone.
	run(t, "", "types", "generate")
	data, err := os.ReadFile(filepath.Join(root, "node_modules/holon-ui/generated/index.d.ts"))
	if err != nil || !strings.Contains(string(data), `"view"`) || strings.Contains(string(data), `"orders"`) {
		t.Fatalf("holon-ui = %s, %v", data, err)
	}
}

func TestConsumerSelectsTheOwnServer(t *testing.T) {
	const browser = `{"dependencies":{"holon-sh":"^0.0.1","holon-ui":"^0.0.1"}}`
	for _, test := range []struct {
		name, manifest string
		servers        []string
		server, alias  string // the own server's directory, relative to the project
		sources        []string
		err            string
	}{
		{name: "a browser project finds its server and the requires beside it", manifest: browser, server: "server", sources: []string{"orders"}},
		{name: "a project without a browser client does not look", manifest: `{"dependencies":{"holon-sh":"^0.0.1"}}`, err: "declares no requires; pass -s"},
		{name: "an alias stays on the own server", manifest: browser, servers: []string{"shop=./server"}, server: "server", alias: "shop"},
		{name: "-s replaces discovery", manifest: browser, servers: []string{"docs=https://mcp.example/mcp"}, sources: []string{"docs"}},
		{name: "one own server", manifest: browser, servers: []string{"./server", "./server/server.py"}, err: "one own server"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Chdir(root)
			writeCLIFile(t, root, "package.json", test.manifest)
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
	} {
		run(t, test.want, test.args...)
	}
	writeCLIFile(t, root, "package.json", `{"dependencies":{"holon-sh":"^0.0.1"}}`)
	run(t, "holon-sh is not installed", "check", "--no-generate")
	installCLISlot(t, root)
	writeCLIFile(t, root, "server.json", `{"name":"example.com/checkout","version":"1.0.0","description":"Checkout"`+requiresOrders)
	run(t, "example.com/orders@1.0.0", "types", "generate")
	run(t, "has no generated clients; run holon types generate", "check", "--no-generate")
}

// requiresOrders ends a server.json that requires an orders server from an unreachable registry.
const requiresOrders = `,"_meta":{"io.modelcontextprotocol.registry/publisher-provided":{"sh.holon":{"requires":{"orders":{"name":"example.com/orders","version":"1.0.0","registry":"https://127.0.0.1:1"}}}}}}`

const pythonServer = "from mcp.server import MCPServer\nserver = MCPServer('provider')\n@server.tool()\ndef read(id: str) -> str:\n    return id\n"

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
	run(t, "--output writes a --contract document", "types", "generate", "-o", committed)
}
