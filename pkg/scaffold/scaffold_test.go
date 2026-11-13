package scaffold

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestCheckRefusesToOverwrite(t *testing.T) {
	for _, language := range Languages {
		t.Run(language, func(t *testing.T) {
			project := Project{Dir: filepath.Join(t.TempDir(), "orders"), Language: language, Name: "com.example/orders"}
			if _, _, _, err := project.Write(); err != nil {
				t.Fatal(err)
			}
			if err := project.Check(); err == nil || !strings.Contains(err.Error(), "orders already has") {
				t.Errorf("Check after Write = %v, want the files it would overwrite", err)
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
	if !strings.Contains(pkg, `"vite": "^7.0.0"`) || !strings.Contains(pkg, `"build": "tsc"`) || strings.Contains(pkg, `"dev:build"`) {
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
	if !strings.Contains(pkg, `"name": "billing"`) || !strings.Contains(pkg, `"build": ""`) || strings.Contains(pkg, `"dev:build"`) {
		t.Errorf("package.json:\n%s", pkg)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "kept package.json's build script") {
		t.Errorf("notes = %v", notes)
	}
}

func TestWriteAddsIndependentBuild(t *testing.T) {
	for _, existing := range []string{"", "custom frontend build"} {
		t.Run(existing, func(t *testing.T) {
			dir := t.TempDir()
			if existing != "" {
				put(t, dir, "package.json", `{"scripts":{"dev:build":"custom frontend build"}}`)
			}
			if _, _, _, err := (Project{Dir: dir, Language: "ts", Name: "com.example/billing"}).Write(); err != nil {
				t.Fatal(err)
			}
			var pkg struct{ Scripts map[string]string }
			if err := json.Unmarshal([]byte(read(t, dir, "package.json")), &pkg); err != nil {
				t.Fatal(err)
			}
			want := existing
			if want == "" {
				want = pkg.Scripts["build"]
			}
			if !strings.Contains(pkg.Scripts["build"], "vite build") || pkg.Scripts["dev:build"] != want {
				t.Fatalf("scripts = %v", pkg.Scripts)
			}
		})
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
		"/holon-ui": `{"versions": {"0.0.1-dev.9": {}, "0.0.1-dev.10": {}, "0.0.1-dev.11": {}}}`,
		"/holon-sh": `{"versions": {"0.0.1-dev.6": {}}}`,
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
		if want := (Versions{HolonUI: "0.0.1-dev.11", HolonPy: "0.0.1.dev5", HolonTypes: holonTypes}); versions != want {
			t.Errorf("py versions from holon %s = %+v, want %+v", cli, versions, want)
		}
	}
	versions, err := local.resolve(context.Background(), "ts", "0.0.1-dev.19")
	if want := (Versions{HolonUI: "0.0.1-dev.11", HolonSH: "0.0.1-dev.6", HolonTypes: "0.0.1-dev.19"}); err != nil || versions != want {
		t.Errorf("ts versions = %+v, %v, want %+v", versions, err, want)
	}
}
