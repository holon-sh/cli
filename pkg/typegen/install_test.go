//go:build kiln

package typegen

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/holon-sh/cli/internal/kiln"
)

func TestGenerateInstallsADeclaredRuntime(t *testing.T) {
	t.Setenv("VIRTUAL_ENV", "")
	bin := t.TempDir()
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	stand := func(dir, first, last string) {
		steps, err := kiln.Development(t.Context(), dir)
		if err != nil {
			t.Fatal(err)
		}
		for i, command := range steps.Install {
			script := "#!/bin/sh\n"
			if i == 0 {
				script += first + "\n"
			}
			if i == len(steps.Install)-1 {
				script += last + "\n"
			}
			put(t, bin, command[0], script)
			if err := os.Chmod(filepath.Join(bin, command[0]), 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}
	installs := func(dir, installed string) { stand(dir, "mkdir -p "+filepath.Dir(installed), "touch "+installed) }
	c, extract := checkoutConsumer(t, TargetTS)
	python, page := t.TempDir(), t.TempDir()
	for _, dir := range []string{python, page} {
		put(t, dir, "pyproject.toml", "[project]\nname = \"app\"\nversion = \"1\"\nrequires-python = \">=3.12\"\ndependencies = [\"holon-sh\"]\n")
		put(t, dir, "uv.lock", "version = 1\nrequires-python = \">=3.12\"\n")
	}
	put(t, page, "package.json", `{"scripts":{"build":"vite build"},"dependencies":{"holon-ui":"^0.0.1"}}`)
	if err := os.MkdirAll(filepath.Join(page, ".venv", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/usr/bin/env", filepath.Join(page, ".venv", "bin", "python")); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		installed string
		consumer  Consumer
	}{
		{"node_modules/holon-sh/package.json", c},
		{".venv/bin/python", Consumer{Name: "app", Dir: python, Targets: []string{TargetPy}}},
		{"node_modules/holon-ui/package.json", Consumer{Name: "page", Dir: page, Targets: []string{TargetUI}}},
	} {
		installs(test.consumer.Dir, test.installed)
		var output bytes.Buffer
		if err := Generate(t.Context(), []Consumer{test.consumer}, Options{Stdout: &output, Stderr: &output, Run: extract}); err != nil || !isFile(filepath.Join(test.consumer.Dir, test.installed)) {
			t.Fatalf("%s: generate: %v\n%s", test.installed, err, &output)
		}
		stand(test.consumer.Dir, "exit 17", "exit 17")
		if err := Generate(t.Context(), []Consumer{test.consumer}, Options{Run: extract}); err != nil {
			t.Fatalf("%s: installed runtime was reinstalled: %v", test.installed, err)
		}
	}
	if err := os.RemoveAll(filepath.Join(c.Dir, "node_modules")); err != nil {
		t.Fatal(err)
	}
	put(t, c.Dir, "package.json", `{"type":"module"}`)
	installs(c.Dir, "node_modules/holon-sh/package.json")
	if err := Generate(t.Context(), []Consumer{c}, Options{Run: extract}); err == nil || isFile(filepath.Join(c.Dir, "node_modules/holon-sh/package.json")) {
		t.Fatalf("undeclared runtime was installed: %v", err)
	}
}
