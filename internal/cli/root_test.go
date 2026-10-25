package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestContractFlagsRequireAnExplicitRemoteIdentity(t *testing.T) {
	for _, args := range [][]string{{"types", "contract", "--url", "https://example.com/mcp"}, {"types", "contract", "app", "--url", "https://example.com/mcp", "--name", "example.com/app", "--version", "1"}, {"types", "contract", "--name", "example.com/app"}} {
		command := NewCommand("test")
		command.SetArgs(args)
		command.SetOut(new(bytes.Buffer))
		command.SetErr(new(bytes.Buffer))
		if err := command.Execute(); err == nil {
			t.Fatalf("accepted conflicting/missing flags: %v", args)
		}
	}
}
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
	for _, args := range [][]string{{"check", "--emit"}, {"check", "--no-published"}, {"types", "generate", "-p", "x"}, {"types", "generate", "--project", "x"}} {
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
	for _, args := range [][]string{{"types", "generate", empty}, {"types", "generate", app, empty}, {"check", app, empty}, {"types", "contract", empty}} {
		err := run(args...)
		if err == nil || !strings.Contains(err.Error(), empty+": no server.json found") || !strings.Contains(err.Error(), "pass DIR...") {
			t.Fatalf("%v = %v", args, err)
		}
	}
	if err := run("types", "contract", app, empty); err == nil || !strings.Contains(err.Error(), "accepts at most 1 arg") {
		t.Fatalf("contract with two directories = %v", err)
	}
}
