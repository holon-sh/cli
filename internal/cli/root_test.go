package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStandaloneVersionAndRemovedEmitFlag(t *testing.T) {
	var output bytes.Buffer
	command := NewCommand("0.0.1")
	command.SetOut(&output)
	command.SetArgs([]string{"--version"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "standalone types CLI") {
		t.Fatalf("ambiguous version: %s", &output)
	}
	for _, args := range [][]string{{"check", "--emit"}, {"check", "--no-published"}, {"types", "generate", "-p", "x"}, {"types", "generate", "--project", "x"}, {"types", "contract"}} {
		command = NewCommand("test")
		command.SetArgs(args)
		command.SetErr(new(bytes.Buffer))
		if err := command.Execute(); err == nil || !strings.Contains(err.Error(), "unknown") {
			t.Fatalf("removed flag accepted: %v: %v", args, err)
		}
	}
}

func run(args ...string) error {
	command := NewCommand("test")
	command.SetArgs(args)
	command.SetOut(new(bytes.Buffer))
	command.SetErr(new(bytes.Buffer))
	return command.Execute()
}

func TestCommandsRequireAServerJSONInEveryDirectory(t *testing.T) {
	empty, app := t.TempDir(), t.TempDir()
	server := `{"name":"example.com/checkout","version":"1.0.0","description":"Checkout"}`
	if err := os.WriteFile(filepath.Join(app, "server.json"), []byte(server), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"types", "generate", empty}, {"types", "generate", app, empty}, {"check", app, empty}, {"types", "check", app, empty}, {"types", "generate", "--contract", empty}} {
		err := run(args...)
		if err == nil || !strings.Contains(err.Error(), empty+": no server.json found") || !strings.Contains(err.Error(), "pass DIR...") {
			t.Fatalf("%v = %v", args, err)
		}
	}
	if err := run("types", "generate", "--contract", app, app); err == nil || !strings.Contains(err.Error(), "--contract takes at most one DIR") {
		t.Fatalf("--contract with two directories = %v", err)
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
	declarations, err := os.ReadFile(filepath.Join(root, "index.d.ts"))
	if err != nil || !strings.Contains(string(declarations), `from "./resources.js"`) || !strings.Contains(string(declarations), `from "holon-core/protocol"`) {
		t.Fatalf("index.d.ts: %v\n%s", err, declarations)
	}
}

func TestCheckShortcutRunsTypesCheck(t *testing.T) {
	dir := t.TempDir()
	short, canonical := run("check", dir), run("types", "check", dir)
	if short == nil || canonical == nil || short.Error() != canonical.Error() || !strings.Contains(short.Error(), "no server.json found") {
		t.Fatalf("holon check = %v, holon types check = %v", short, canonical)
	}
	if err := run("check", "--no-published"); err == nil || err.Error() != run("types", "check", "--no-published").Error() {
		t.Fatalf("flags differ between holon check and holon types check: %v", err)
	}
}
