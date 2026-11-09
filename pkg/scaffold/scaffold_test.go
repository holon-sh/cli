package scaffold

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/holon-sh/cli/pkg/typegen"
)

func TestWriteRefusesToOverwrite(t *testing.T) {
	for _, language := range Languages {
		t.Run(language, func(t *testing.T) {
			project := Project{Dir: filepath.Join(t.TempDir(), "orders"), Language: language, Name: "com.example/orders"}
			if _, _, _, err := project.Write(); err != nil {
				t.Fatal(err)
			}
			if _, _, _, err := project.Write(); err == nil || !strings.Contains(err.Error(), "orders already has") {
				t.Errorf("second Write = %v, want the files it would overwrite", err)
			}
		})
	}
}

func TestWriteBesideAProject(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "billing")
	put(t, dir, "pyproject.toml", "[project]\nname = \"billing\"\n")
	put(t, dir, ".gitignore", ".venv\n")
	put(t, dir, "package.json", "{\n  \"name\": \"billing\",\n  \"scripts\": {\n    \"build\": \"tsc\"\n  },\n  \"devDependencies\": {\n    \"vite\": \"^7.0.0\"\n  }\n}\n")
	project := Project{Dir: dir, Language: "py", Name: "com.example/billing"}
	created, changed, notes, err := project.Write()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(created, "server.py") || slices.Contains(created, ".python-version") || !slices.Equal(changed, []string{".gitignore", "package.json"}) {
		t.Errorf("created %v, changed %v", created, changed)
	}
	if ignore := read(t, dir, ".gitignore"); !strings.HasPrefix(ignore, ".venv\n__pycache__/\n") || strings.Contains(ignore, ".venv/") {
		t.Errorf(".gitignore:\n%s", ignore)
	}
	pkg := read(t, dir, "package.json")
	if !strings.Contains(pkg, `"vite": "^7.0.0"`) || !strings.Contains(pkg, `"build": "tsc"`) {
		t.Errorf("package.json:\n%s", pkg)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "build script") {
		t.Errorf("notes = %v", notes)
	}
}

func TestWriteKeepsAnEmptyBuildScript(t *testing.T) {
	dir := t.TempDir()
	put(t, dir, "package.json", `{"name": "billing", "scripts": {"build": ""}}`)
	project := Project{Dir: dir, Language: "ts", Name: "com.example/billing"}
	_, _, notes, err := project.Write()
	if err != nil {
		t.Fatal(err)
	}
	pkg := read(t, dir, "package.json")
	if !strings.Contains(pkg, `"name": "billing"`) || !strings.Contains(pkg, `"build": ""`) {
		t.Errorf("package.json:\n%s", pkg)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "kept package.json's build script") {
		t.Errorf("notes = %v", notes)
	}
}

func TestCheckRefusesWhatHolonWouldNotRun(t *testing.T) {
	for _, test := range []struct {
		name, language string
		files          map[string]string
		err            string
	}{
		{"start script", "ts", map[string]string{"package.json": `{"scripts": {"start": "node app.js"}}`}, "Holon would run instead of server.ts"},
		{"poetry", "py", map[string]string{"pyproject.toml": "", "poetry.lock": ""}, "has poetry.lock"},
		{"server entry", "py", map[string]string{"pyproject.toml": "", "src/server.py": "import mcp"}, "already has src/server.py"},
		{"no manifest", "ts", map[string]string{"pyproject.toml": ""}, "has no package.json"},
		{"go package", "go", map[string]string{"go.mod": "module m\n", "main.go": "package main"}, "already has main.go"},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, data := range test.files {
				put(t, dir, name, data)
			}
			err := Project{Dir: dir, Language: test.language, Name: "com.example/a"}.Check()
			if err == nil || !strings.Contains(err.Error(), test.err) {
				t.Errorf("Check = %v, want %q", err, test.err)
			}
		})
	}
}

func put(t *testing.T, dir, name, data string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, dir, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestResolve(t *testing.T) {
	npm := map[string]string{
		"/holon-ui": fmt.Sprintf(`{"versions": {"0.0.1-dev.9": {"holon": {"contractVersion": %d}}, "0.0.1-dev.10": {"holon": {"contractVersion": %d}}, "0.0.1-dev.11": {"holon": {"contractVersion": %d}}}}`, typegen.ContractVersion, typegen.ContractVersion, typegen.ContractVersion+1),
		"/holon-sh": fmt.Sprintf(`{"versions": {"0.0.1-dev.6": {"holon": {"contractVersion": %d}}}}`, typegen.ContractVersion-1),
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/pypi/holon-sh/json" {
			_, _ = w.Write([]byte(`{"info": {"version": "0.0.1.dev5"}}`))
		} else if r.URL.Path == "/pypi/holon-types/json" {
			_, _ = w.Write([]byte(`{"info": {"version": "0.0.1.dev20"}}`))
		} else if body, ok := npm[r.URL.Path]; ok {
			_, _ = w.Write([]byte(body))
		} else {
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	local := registries{npm: server.URL, pypi: server.URL}

	for cli, holonTypes := range map[string]string{"0.0.1-dev.19": "0.0.1-dev.19", "0.0.1-dev.15+1152a1a": "0.0.1.dev20", "dev": "0.0.1.dev20"} {
		versions, err := local.resolve(context.Background(), "py", cli)
		if err != nil {
			t.Fatal(err)
		}
		if want := (Versions{HolonUI: "0.0.1-dev.10", HolonPy: "0.0.1.dev5", HolonTypes: holonTypes}); versions != want {
			t.Errorf("py versions from holon %s = %+v, want %+v", cli, versions, want)
		}
	}
	if _, err := local.resolve(context.Background(), "ts", "0.0.1-dev.19"); err == nil || !strings.Contains(err.Error(), "npm holon-sh: no release reads the clients this holon writes") {
		t.Errorf("ts Resolve = %v, want no holon-sh release for this contract", err)
	}
}
