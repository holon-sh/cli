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
		{arg: "c7=http://localhost:3001/mcp", want: typegen.Source{Alias: "c7", Endpoint: "http://localhost:3001/mcp"}},
		{arg: "weird=value=../orders", want: typegen.Source{Alias: "weird", Path: filepath.Join(cwd, "value=../orders")}},
	} {
		got, err := parseSource(test.arg)
		if err != nil || !reflect.DeepEqual(got, test.want) {
			t.Errorf("parseSource(%q) = %+v, %v; want %+v", test.arg, got, err, test.want)
		}
	}
}

func TestRemovedFlagsAndContractArity(t *testing.T) {
	if output := run(t, "", "--version"); !strings.Contains(output, "standalone types CLI") {
		t.Fatalf("version = %q", output)
	}
	for _, args := range [][]string{{"check", "--emit"}, {"check", "--no-published"}, {"types", "generate", "-p", "x"}, {"types", "generate", "--no-install", "x"}, {"types", "generate", "--quiet", "x"}, {"types", "generate", "--target", "ts", "x"}, {"types", "check", "--target", "ts"}, {"types", "contract"}, {"types", "compile"}} {
		run(t, "unknown", args...)
	}
	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{"types", "generate", "--contract"}, "needs an argument"},
		{[]string{"types", "generate", "--contract", ""}, "a server path or URL is required"},
		{[]string{"types", "generate", "--contract", "orders="}, "a server path or URL is required"},
		{[]string{"types", "generate", "--contract", "a", "-s", "b"}, "takes one SERVER and nothing else"},
		{[]string{"types", "generate", "--contract", "a", "somedir"}, "takes one SERVER and nothing else"},
	} {
		run(t, test.want, test.args...)
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
	writeCLIFile(t, root, "node_modules/holon-sh/package.json", `{"name":"holon-sh","exports":{".":{"types":"./generated/index.d.ts"}},"holon":{"slot":"generated","contractVersion":1}}`)
	writeCLIFile(t, root, "node_modules/holon-sh/generated/holon-generated.json", `{"marker":"`+mcpcontract.Marker+`","placeholder":true}`)
}

func TestExplicitOwnServer(t *testing.T) {
	for _, test := range []struct {
		name          string
		entries, args []string
		want          string
	}{
		{"own server only", []string{"server.ts"}, []string{"-s", "./server.ts"}, "has no requires"},
		{"two own servers", []string{"server.ts", "src/server.ts"}, []string{"-s", "./server.ts", "-s", "./src/server.ts"}, "one own server"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			writeCLIFile(t, root, "package.json", `{"dependencies":{"holon-sh":"^0.0.1"}}`)
			for _, entry := range test.entries {
				writeCLIFile(t, root, entry, "export default {};\n")
			}
			args := append([]string{"types", "generate"}, test.args...)
			run(t, test.want, args...)
		})
	}
}

func TestOwnServerSuppliesConsumerMetadata(t *testing.T) {
	// clientDir hands consumer a symlink-free directory; do the same here.
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	writeCLIFile(t, root, "server.ts", "export default {};\n")
	writeCLIFile(t, root, "package.json", `{"dependencies":{"holon-sh":"^0.0.1"}}`)
	c, err := consumer(root, "", []string{"./server.ts", "../orders"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Server == nil || c.Server.Path != filepath.Join(root, "server.ts") || len(c.Sources) != 1 || c.Sources[0].Path != filepath.Join(root, "../orders") {
		t.Fatalf("consumer = %+v", c)
	}
}

func TestCheckGrammar(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{"check", "a", "b"}, "one DIR at most; dependencies are -s values"},
		{[]string{"types", "check", "a", "b"}, "one DIR at most; dependencies are -s values"},
		{[]string{"types", "generate", "a", "b"}, "one DIR at most; dependencies are -s values"},
		{[]string{"check", "--no-generate", "-s", "x"}, "-s has no effect"},
		{[]string{"check", "missing-dir"}, "is not a directory"},
	} {
		run(t, test.want, test.args...)
	}
	writeCLIFile(t, root, "package.json", `{"dependencies":{"holon-sh":"^0.0.1"}}`)
	for _, args := range [][]string{{"check", "--no-generate"}, {"types", "check", "--no-generate"}} {
		run(t, "holon-sh is not installed", args...)
	}
	installCLISlot(t, root)
	for _, args := range [][]string{{"check"}, {"types", "check"}} {
		run(t, "has no requires in server.json; pass -s", args...)
		run(t, "has no generated clients; run holon types generate", append(args, "--no-generate")...)
	}
	run(t, "is not a directory", "check", "package.json")
}

func TestOldPositionalServerHint(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "orders/server.py", "")
	writeCLIFile(t, root, "checkout/server.ts", "")
	t.Chdir(filepath.Join(root, "checkout"))
	for _, test := range []struct {
		args []string
		hint bool
	}{
		{[]string{"../orders"}, true},
		{[]string{"."}, true},
		{nil, false},
		{[]string{"../orders", "-s", "../another"}, false},
	} {
		message := run(t, "declares no runtime", append([]string{"types", "generate"}, test.args...)...)
		if strings.Contains(message, "did you mean -s") != test.hint {
			t.Errorf("generate %v = %s, want hint %v", test.args, message, test.hint)
		}
	}
	writeCLIFile(t, root, "orders/package.json", "{")
	if message := run(t, "unexpected end of JSON input", "types", "generate", "../orders"); strings.Contains(message, "did you mean") {
		t.Errorf("malformed manifest = %s, want no old-form hint", message)
	}
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
}

func TestGenerateReadsRequires(t *testing.T) {
	root := t.TempDir()
	client := filepath.Join(root, "checkout")
	writeCLIFile(t, client, "package.json", `{"type":"module","dependencies":{"holon-sh":"^0.0.1"}}`)
	writeCLIFile(t, client, "server.json", `{"name":"example.com/checkout","version":"1.0.0","description":"Checkout","_meta":{"io.modelcontextprotocol.registry/publisher-provided":{"sh.holon":{"requires":{"orders":{"name":"example.com/orders","version":"1.0.0","registry":"https://127.0.0.1:1"}}}}}}`)
	// A ts-only client can have an entry without treating it as its own server.
	writeCLIFile(t, client, "server.ts", "export const answer: string = 'ready';\n")
	installCLISlot(t, client)
	for _, name := range []string{"orders", "docs,archive"} {
		writeCLIFile(t, root, name+"/server.json", `{"name":"example.com/orders","version":"1.0.0","description":"Orders"}`)
		writeCLIFile(t, root, name+"/server.py", pythonServer)
	}
	t.Chdir(client)
	run(t, "example.com/orders@1.0.0", "types", "generate")
	run(t, "", "types", "generate", "-s", "../orders")
	slot := filepath.Join(client, "node_modules/holon-sh/generated")
	data, err := os.ReadFile(filepath.Join(slot, "holon-generated.json"))
	if err != nil {
		t.Fatal(err)
	}
	var metadata struct {
		App string `json:"app"`
	}
	if err := json.Unmarshal(data, &metadata); err != nil || metadata.App != "checkout" {
		t.Fatalf("generated slot metadata = %s, %v", data, err)
	}

	// Check must regenerate before checking, accept DIR through a symlink, and resolve
	// repeatable -s values (including a comma in a path) against the working directory.
	if err := os.RemoveAll(slot); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(client, filepath.Join(root, "client-link")); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	output := run(t, "", "types", "check", "-s=orders", "client-link", "-sdocs=docs,archive")
	if output != "Typed contracts check passed.\n" {
		t.Fatalf("check with explicit dependencies = %q", output)
	}
	data, err = os.ReadFile(filepath.Join(slot, "index.d.ts"))
	if err != nil || !strings.Contains(string(data), "orders") || !strings.Contains(string(data), "docs") {
		t.Fatalf("repeatable dependencies not generated: %s, %v", data, err)
	}
	before, err := os.Stat(filepath.Join(slot, "holon-generated.json"))
	if err != nil {
		t.Fatal(err)
	}
	run(t, "", "check", "client-link", "--no-generate")
	after, err := os.Stat(filepath.Join(slot, "holon-generated.json"))
	if err != nil || !after.ModTime().Equal(before.ModTime()) {
		t.Fatalf("offline check changed the generated slot: %v", err)
	}
}
