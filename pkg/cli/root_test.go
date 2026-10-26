package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
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
		if got.Alias != test.want.Alias || got.Path != test.want.Path || got.Endpoint != test.want.Endpoint || (got.Registry == nil) != (test.want.Registry == nil) || got.Registry != nil && *got.Registry != *test.want.Registry {
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
	for _, args := range [][]string{{"types", "generate", "--contract"}, {"types", "generate", "--contract", "a", "b"}} {
		if _, err := run(t, args...); err == nil || !strings.Contains(err.Error(), "exactly one SERVER") {
			t.Errorf("%v = %v", args, err)
		}
	}
}

func TestTargetsComeFromTheProjectsDeclarations(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "server.ts"), []byte("export default {};\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src.ts"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	for _, test := range []struct {
		manifest string
		args     []string
		want     string
	}{
		{"", []string{"../orders"}, "declares no runtime"},
		{`{"dependencies":{"holon-sh":"^0.0.1"}}`, nil, "at least one SERVER"},
		{`{"dependencies":{"holon-sh":"^0.0.1"}}`, []string{"./server.ts"}, "does not depend on holon-ui"},
		{`{"dependencies":{"holon-ui":"^0.0.1"}}`, []string{"../orders"}, "list its own server"},
		{`{"dependencies":{"holon-ui":"^0.0.1"}}`, []string{"./server.ts", "./src.ts"}, "one own server"},
	} {
		if test.manifest == "" {
			_ = os.Remove(filepath.Join(root, "package.json"))
		} else if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(test.manifest), 0o644); err != nil {
			t.Fatal(err)
		}
		args := append([]string{"types", "generate"}, test.args...)
		if _, err := run(t, args...); err == nil || !strings.Contains(err.Error(), test.want) {
			t.Errorf("%v with %s = %v, want %q", args, test.manifest, err, test.want)
		}
	}
}

func TestCheckTakesNoArgumentsAndNeedsGeneratedClients(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	for _, args := range [][]string{{"check", "orders"}, {"types", "check", "../orders"}} {
		if _, err := run(t, args...); err == nil || !strings.Contains(err.Error(), "unknown command") {
			t.Errorf("%v = %v, want an unknown command error", args, err)
		}
	}
	if _, err := run(t, "check"); err == nil || !strings.Contains(err.Error(), "declares no runtime") {
		t.Fatalf("check without a manifest = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"dependencies":{"holon-sh":"^0.0.1"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"check"}, {"types", "check"}} {
		if _, err := run(t, args...); err == nil || !strings.Contains(err.Error(), "holon-sh is not installed") {
			t.Errorf("%v without the slot = %v", args, err)
		}
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
