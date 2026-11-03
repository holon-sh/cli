package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

func TestInvalidLaunchInputs(t *testing.T) {
	for _, test := range []struct {
		command []string
		want    string
	}{
		{nil, "missing MCP executable"},
		{[]string{""}, "missing MCP executable"},
		{[]string{"/missing/sensitive"}, "could not start MCP server"},
	} {
		err := run(context.Background(), append([]string{"--"}, test.command...))
		if err == nil || !strings.Contains(err.Error(), test.want) || strings.Contains(err.Error(), "sensitive") {
			t.Fatalf("expected %s without values, got %v", test.want, err)
		}
	}
}

func TestNativeHTTPExec(t *testing.T) {
	switch os.Getenv("HOLON_EXEC_TEST") {
	case "launcher":
		if len(os.Args) > 2 && os.Args[2] == "--" {
			t.Setenv("HOLON_EXEC_TEST", "child")
			TestNativeHTTPExec(t)
			return
		}
		args := []string{"--transport=streamable-http", "--", os.Args[0], "-test.run=TestNativeHTTPExec", "--", "--project=two words;$(not-a-shell)", "from-snapshot"}
		if err := run(context.Background(), args); err != nil {
			os.Exit(90)
		}
	case "child":
		data, _ := json.Marshal(map[string]any{"args": os.Args[3:], "value": os.Getenv("RUNTIME_VALUE")})
		_, _ = os.Stdout.Write(data)
		os.Exit(7)
	}
	if os.Getenv("HOLON_EXEC_TEST") != "" {
		return
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), binary, "-test.run=TestNativeHTTPExec")
	command.Env = append(os.Environ(), "HOLON_EXEC_TEST=launcher", "RUNTIME_VALUE=from-snapshot")
	output, err := command.CombinedOutput()
	var exited *exec.ExitError
	if !errors.As(err, &exited) || exited.ExitCode() != 7 {
		t.Fatalf("native process exit was not preserved: %v %s", err, output)
	}
	var result struct {
		Args  []string `json:"args"`
		Value string   `json:"value"`
	}
	want := []string{"--project=two words;$(not-a-shell)", "from-snapshot"}
	if json.Unmarshal(output, &result) != nil || !slices.Equal(result.Args, want) || result.Value != "from-snapshot" {
		t.Fatalf("native argv/environment contract failed: %s", output)
	}
}
