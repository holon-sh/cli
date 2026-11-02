package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
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

func TestParseSource(t *testing.T) {
	cwd, _ := os.Getwd()
	for _, test := range []struct {
		arg  string
		want typegen.Source
	}{
		{arg: "../orders", want: typegen.Source{Path: filepath.Join(cwd, "../orders")}},
		{arg: "orders=../orders/server.py", want: typegen.Source{Alias: "orders", Path: filepath.Join(cwd, "../orders/server.py")}},
		{arg: "https://registry.modelcontextprotocol.io/v0.1/servers/io.github.upstash%2Fcontext7/versions/4.4.4", want: typegen.Source{Registry: &typegen.Reference{Name: "io.github.upstash/context7", Version: "4.4.4", Registry: "https://registry.modelcontextprotocol.io"}}},
		{arg: "docs=https://registry.example/v0/servers/example.com%2Fdocs/versions/1.0.0", want: typegen.Source{Alias: "docs", Registry: &typegen.Reference{Name: "example.com/docs", Version: "1.0.0", Registry: "https://registry.example"}}},
		{arg: "https://mcp.context7.com/mcp", want: typegen.Source{Endpoint: "https://mcp.context7.com/mcp"}},
	} {
		got, err := parseSource(test.arg)
		if err != nil || !reflect.DeepEqual(got, test.want) {
			t.Errorf("parseSource(%q) = %+v, %v; want %+v", test.arg, got, err, test.want)
		}
	}
	if _, err := parseSource("orders="); err == nil {
		t.Error("parseSource accepted an alias without a server")
	}
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

func TestExplicitOwnServer(t *testing.T) {
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
		if err != nil || strings.Contains(string(data), `"view"`) != want || strings.Contains(string(data), "class App") != want || !strings.Contains(string(data), `"orders"`) {
			t.Fatalf("%s = %s, %v", pkg, data, err)
		}
	}
	writeCLIFile(t, root, "server.json", `{"name":"example.com/---","version":"1.0.0","description":"View"}`)
	run(t, "", "types", "generate", "-s", "./server.py")
	if data, err := os.ReadFile(filepath.Join(root, "node_modules/holon-ui/generated/index.d.ts")); err != nil || !strings.Contains(string(data), `Apps["app"]["tools"]`) {
		t.Fatalf("punctuation-only server name lost its App class: %s, %v", data, err)
	}
	run(t, "one own server", "types", "generate", "-s", "./server.py", "-s", "./server.py")
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
	run(t, "declares no requires; pass -s", "check")
	run(t, "has no generated clients; run holon types generate", "check", "--no-generate")
}

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

func TestGenerateReadsRequires(t *testing.T) {
	compiler, err := filepath.Abs("../../tooling/typescript/node_modules/typescript")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	client := filepath.Join(root, "checkout")
	writeCLIFile(t, client, "package.json", `{"type":"module","dependencies":{"holon-sh":"^0.0.1"}}`)
	writeCLIFile(t, client, "server.json", `{"name":"example.com/checkout","version":"1.0.0","description":"Checkout","_meta":{"io.modelcontextprotocol.registry/publisher-provided":{"sh.holon":{"requires":{"orders":{"name":"example.com/orders","version":"1.0.0","registry":"https://127.0.0.1:1"}}}}}}`)
	// A ts-only client can have an entry without treating it as its own server.
	writeCLIFile(t, client, "server.ts", "export const answer: string = 'ready';\n")
	installCLISlot(t, client)
	if err := os.Symlink(compiler, filepath.Join(client, "node_modules/typescript")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"orders", "docs,archive"} {
		writeCLIFile(t, root, name+"/server.json", `{"name":"example.com/orders","version":"1.0.0","description":"Orders"}`)
		writeCLIFile(t, root, name+"/server.py", pythonServer)
	}
	t.Chdir(client)
	run(t, "example.com/orders@1.0.0", "types", "generate")

	// Check must generate before checking, accept DIR through a symlink, and resolve
	// repeatable -s values (including a comma in a path) against the working directory.
	if err := os.Symlink(client, filepath.Join(root, "client-link")); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	output := run(t, "", "types", "check", "-s=orders", "client-link", "-sdocs=docs,archive")
	if output != "Typed contracts check passed.\n" {
		t.Fatalf("check with explicit dependencies = %q", output)
	}
	data, err := os.ReadFile(filepath.Join(client, "node_modules/holon-sh/generated/index.d.ts"))
	if err != nil || !strings.Contains(string(data), "orders") || !strings.Contains(string(data), "docs") {
		t.Fatalf("repeatable dependencies not generated: %s, %v", data, err)
	}
	run(t, "", "check", "client-link", "--no-generate")
}
