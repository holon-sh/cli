//go:build kiln

package typegen

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestGenerateInstallsADeclaredRuntime(t *testing.T) {
	t.Setenv("VIRTUAL_ENV", "")
	c, extract := checkoutConsumer(t, TargetTS)
	python := t.TempDir()
	put(t, python, "pyproject.toml", "[project]\nname = \"app\"\nversion = \"1\"\nrequires-python = \">=3.12\"\ndependencies = [\"holon-sh\"]\n")
	put(t, python, "uv.lock", "version = 1\nrequires-python = \">=3.12\"\n")
	for _, test := range []struct {
		installed string
		consumer  Consumer
	}{
		{"node_modules/holon-sh/package.json", c},
		{".venv/bin/python", Consumer{Name: "app", Dir: python, Targets: []string{TargetPy}}},
	} {
		configure := func(commands ...string) {
			put(t, test.consumer.Dir, "railpack.json", string(mustJSON(t, map[string]any{"steps": map[string]any{"install": map[string]any{"commands": commands}}})))
		}
		configure("mkdir -p "+filepath.Dir(test.installed), "touch "+test.installed)
		var output bytes.Buffer
		if err := Generate(t.Context(), []Consumer{test.consumer}, Options{Stdout: &output, Stderr: &output, Run: extract}); err != nil || !isFile(filepath.Join(test.consumer.Dir, test.installed)) {
			t.Fatalf("%s: generate: %v\n%s", test.installed, err, &output)
		}
		configure("false")
		if err := Generate(t.Context(), []Consumer{test.consumer}, Options{Run: extract}); err != nil {
			t.Fatalf("%s: installed runtime was reinstalled: %v", test.installed, err)
		}
	}
	if err := os.RemoveAll(filepath.Join(c.Dir, "node_modules")); err != nil {
		t.Fatal(err)
	}
	put(t, c.Dir, "package.json", `{"type":"module"}`)
	put(t, c.Dir, "railpack.json", `{"steps":{"install":{"commands":["mkdir -p node_modules/holon-sh","touch node_modules/holon-sh/package.json"]}}}`)
	if err := Generate(t.Context(), []Consumer{c}, Options{Run: extract}); err == nil || isFile(filepath.Join(c.Dir, "node_modules/holon-sh/package.json")) {
		t.Fatalf("undeclared runtime was installed: %v", err)
	}
}
