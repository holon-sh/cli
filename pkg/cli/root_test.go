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

func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var output bytes.Buffer
	command := NewCommand("test")
	command.SetArgs(args)
	command.SetOut(&output)
	command.SetErr(new(bytes.Buffer))
	err := command.Execute()
	return output.String(), err
}

func TestParseSource(t *testing.T) {
	cwd, _ := os.Getwd()
	for _, test := range []struct {
		arg  string
		want typegen.Source
		err  string
	}{
		{arg: "../orders", want: typegen.Source{Path: filepath.Join(cwd, "../orders")}},
		{arg: "orders=../orders/server.py", want: typegen.Source{Alias: "orders", Path: filepath.Join(cwd, "../orders/server.py")}},
		{arg: "https://registry.modelcontextprotocol.io/v0.1/servers/io.github.upstash%2Fcontext7/versions/4.4.4", want: typegen.Source{Registry: &typegen.Reference{Name: "io.github.upstash/context7", Version: "4.4.4", Registry: "https://registry.modelcontextprotocol.io"}}},
		{arg: "docs=https://registry.example/v0/servers/example.com%2Fdocs/versions/1.0.0", want: typegen.Source{Alias: "docs", Registry: &typegen.Reference{Name: "example.com/docs", Version: "1.0.0", Registry: "https://registry.example"}}},
		{arg: "https://mcp.context7.com/mcp", want: typegen.Source{Endpoint: "https://mcp.context7.com/mcp"}},
		{arg: "c7=http://localhost:3001/mcp", want: typegen.Source{Alias: "c7", Endpoint: "http://localhost:3001/mcp"}},
		{arg: "weird=value=../orders", want: typegen.Source{Alias: "weird", Path: filepath.Join(cwd, "value=../orders")}},
		{arg: "orders=", err: "a server is required after ALIAS="},
	} {
		got, err := parseSource(test.arg)
		if test.err != "" {
			if err == nil || !strings.Contains(err.Error(), test.err) {
				t.Errorf("parseSource(%q) = %v, want %q", test.arg, err, test.err)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseSource(%q): %v", test.arg, err)
			continue
		}
		if !reflect.DeepEqual(got, test.want) {
			t.Errorf("parseSource(%q) = %+v, want %+v", test.arg, got, test.want)
		}
	}
}

func TestRemovedFlagsAndContractArity(t *testing.T) {
	output, err := run(t, "--version")
	if err != nil || !strings.Contains(output, "standalone types CLI") {
		t.Fatalf("version = %q, %v", output, err)
	}
	for _, args := range [][]string{{"check", "--emit"}, {"check", "--no-published"}, {"types", "generate", "-p", "x"}, {"types", "generate", "--no-install", "x"}, {"types", "generate", "--quiet", "x"}, {"types", "generate", "--target", "ts", "x"}, {"types", "check", "--target", "ts"}, {"types", "contract"}} {
		if _, err := run(t, args...); err == nil || !strings.Contains(err.Error(), "unknown") {
			t.Fatalf("removed flag accepted: %v: %v", args, err)
		}
	}
	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{"types", "generate", "--contract"}, "needs an argument"},
		{[]string{"types", "generate", "--contract", "a", "-s", "b"}, "takes one SERVER and nothing else"},
		{[]string{"types", "generate", "--contract", "a", "somedir"}, "takes one SERVER and nothing else"},
	} {
		if _, err := run(t, test.args...); err == nil || !strings.Contains(err.Error(), test.want) {
			t.Errorf("%v = %v, want %q", test.args, err, test.want)
		}
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

func installCLISlot(t *testing.T, root, pkg string) {
	t.Helper()
	writeCLIFile(t, root, "node_modules/"+pkg+"/package.json", `{"name":"`+pkg+`","exports":{".":{"types":"./generated/index.d.ts"}},"holon":{"slot":"generated","contractVersion":1}}`)
	writeCLIFile(t, root, "node_modules/"+pkg+"/generated/holon-generated.json", `{"marker":"`+mcpcontract.Marker+`","placeholder":true}`)
}

func TestTargetsComeFromTheProjectsDeclarations(t *testing.T) {
	for _, test := range []struct {
		name, manifest string
		entries, args  []string
		want           string
	}{
		{"no runtime", "", []string{"server.ts"}, nil, "declares no runtime"},
		{"no requires", `{"dependencies":{"holon-sh":"^0.0.1"}}`, []string{"server.ts"}, nil, "has no requires"},
		{"own server only", `{"dependencies":{"holon-sh":"^0.0.1"}}`, []string{"server.ts"}, []string{"-s", "./server.ts"}, "has no requires"},
		{"two own servers", `{"dependencies":{"holon-sh":"^0.0.1"}}`, []string{"server.ts", "src/server.ts"}, []string{"-s", "./server.ts", "-s", "./src/server.ts"}, "one own server"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			if test.manifest != "" {
				writeCLIFile(t, root, "package.json", test.manifest)
			}
			for _, entry := range test.entries {
				writeCLIFile(t, root, entry, "export default {};\n")
			}
			args := append([]string{"types", "generate"}, test.args...)
			if _, err := run(t, args...); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Errorf("%v = %v, want %q", args, err, test.want)
			}
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
		{[]string{"types", "generate", "missing-dir"}, "is not a directory"},
		{[]string{"check"}, "declares no runtime"},
	} {
		if _, err := run(t, test.args...); err == nil || !strings.Contains(err.Error(), test.want) {
			t.Errorf("%v = %v, want %q", test.args, err, test.want)
		}
	}
	writeCLIFile(t, root, "package.json", `{"dependencies":{"holon-sh":"^0.0.1"}}`)
	for _, args := range [][]string{{"check", "--no-generate"}, {"types", "check", "--no-generate"}} {
		if _, err := run(t, args...); err == nil || !strings.Contains(err.Error(), "holon-sh is not installed") {
			t.Errorf("%v without the slot = %v", args, err)
		}
	}
	installCLISlot(t, root, "holon-sh")
	for _, args := range [][]string{{"check"}, {"types", "check"}} {
		if _, err := run(t, args...); err == nil || !strings.Contains(err.Error(), "has no requires in server.json; pass -s") {
			t.Errorf("%v without requires = %v", args, err)
		}
		if _, err := run(t, append(args, "--no-generate")...); err == nil || !strings.Contains(err.Error(), "has no generated clients; run holon types generate") {
			t.Errorf("%v with a placeholder slot = %v", args, err)
		}
	}
	if _, err := run(t, "check", "package.json"); err == nil || !strings.Contains(err.Error(), "is not a directory") {
		t.Errorf("file DIR = %v", err)
	}
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
		_, err := run(t, append([]string{"types", "generate"}, test.args...)...)
		if err == nil || !strings.Contains(err.Error(), "declares no runtime") || strings.Contains(err.Error(), "did you mean -s") != test.hint {
			t.Errorf("generate %v = %v, want hint %v", test.args, err, test.hint)
		}
	}
	writeCLIFile(t, root, "orders/package.json", "{")
	if _, err := run(t, "types", "generate", "../orders"); err == nil || strings.Contains(err.Error(), "did you mean") {
		t.Errorf("malformed manifest = %v, want no old-form hint", err)
	}
}

func TestTypesCompileNamesBrowserEntryFiles(t *testing.T) {
	root := t.TempDir()
	command := NewCommand("test")
	command.SetOut(new(bytes.Buffer))
	command.SetIn(strings.NewReader(`{"target":"browser","identity":{"name":"checkout","version":"1.0.0"},"contracts":{"orders":{"tools":[{"name":"read","inputSchema":{"type":"object"}}]}},"browser_runtime_specifier":"../runtime.mjs","protocol_specifier":"holon-core/protocol","entry":"index"}`))
	command.SetArgs([]string{"types", "compile", "--out", root})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if got := strings.Join(names, " "); got != "index.d.ts index.js resources.d.ts" {
		t.Fatalf("compiled files = %s", got)
	}
}

func TestContractOfAPathSource(t *testing.T) {
	dir := t.TempDir()
	for name, data := range map[string]string{
		"server.json": `{"name":"example.com/provider","version":"1.0.0","description":"Provider"}`,
		"server.py":   "from mcp.server import MCPServer\nserver = MCPServer('provider')\n@server.tool()\ndef read(id: str) -> str:\n    return id\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	output, err := run(t, "types", "generate", "--contract", dir)
	if err != nil {
		t.Fatal(err)
	}
	var metadata mcpcontract.Metadata
	if err := json.Unmarshal([]byte(output), &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata.Identity.Name != "example.com/provider" || metadata.Contract == nil || len(metadata.Contract.Tools) != 1 || metadata.Contract.Tools[0].Name != "read" {
		t.Fatalf("provider contract = %s", output)
	}
	if _, err := run(t, "types", "generate", "--contract", filepath.Join(dir, "server.py")); err != nil {
		t.Fatalf("entry file source: %v", err)
	}
}

func TestGenerateReadsRequires(t *testing.T) {
	root := t.TempDir()
	client := filepath.Join(root, "checkout")
	writeCLIFile(t, client, "package.json", `{"type":"module","dependencies":{"holon-sh":"^0.0.1"}}`)
	writeCLIFile(t, client, "server.json", `{"name":"example.com/checkout","version":"1.0.0","description":"Checkout","_meta":{"io.modelcontextprotocol.registry/publisher-provided":{"sh.holon":{"requires":{"orders":{"name":"example.com/orders","version":"1.0.0","registry":"https://127.0.0.1:1"}}}}}}`)
	// A ts-only client can have an entry without treating it as its own server.
	writeCLIFile(t, client, "server.ts", "export const answer: string = 'ready';\n")
	installCLISlot(t, client, "holon-sh")
	for _, name := range []string{"orders", "docs,archive"} {
		writeCLIFile(t, root, name+"/server.json", `{"name":"example.com/orders","version":"1.0.0","description":"Orders"}`)
		writeCLIFile(t, root, name+"/server.py", "from mcp.server import MCPServer\nserver = MCPServer('orders')\n@server.tool()\ndef read(id: str) -> str:\n    return id\n")
	}
	t.Chdir(client)
	for _, args := range [][]string{{"types", "generate"}, {"check"}} {
		if _, err := run(t, args...); err == nil || !strings.Contains(err.Error(), "example.com/orders@1.0.0") {
			t.Fatalf("%v with requires = %v, want dependency resolution error", args, err)
		}
	}
	if _, err := run(t, "types", "generate", "-s", "../orders"); err != nil {
		t.Fatalf("explicit dependency should replace requires: %v", err)
	}
	slot := filepath.Join(client, "node_modules/holon-sh/generated")
	if _, err := os.Stat(filepath.Join(slot, "index.d.ts")); err != nil {
		t.Fatal(err)
	}
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
	output, err := run(t, "types", "check", "-s=orders", "client-link", "-sdocs=docs,archive")
	if err != nil || output != "Typed contracts check passed.\n" {
		t.Fatalf("check with explicit dependencies = %q, %v", output, err)
	}
	data, err = os.ReadFile(filepath.Join(slot, "index.d.ts"))
	if err != nil || !strings.Contains(string(data), "orders") || !strings.Contains(string(data), "docs") {
		t.Fatalf("repeatable dependencies not generated: %s, %v", data, err)
	}
	before, err := os.Stat(filepath.Join(slot, "holon-generated.json"))
	if err != nil {
		t.Fatal(err)
	}
	output, err = run(t, "check", "client-link", "--no-generate")
	if err != nil || output != "Typed contracts check passed.\n" {
		t.Fatalf("offline check = %q, %v", output, err)
	}
	after, err := os.Stat(filepath.Join(slot, "holon-generated.json"))
	if err != nil || !after.ModTime().Equal(before.ModTime()) {
		t.Fatalf("offline check changed the generated slot: %v", err)
	}
}
