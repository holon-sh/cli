package cli

import (
	"bytes"
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
	command = NewCommand("test")
	command.SetArgs([]string{"check", "--emit"})
	command.SetErr(new(bytes.Buffer))
	if err := command.Execute(); err == nil || !strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("legacy emit flag accepted: %v", err)
	}
}
