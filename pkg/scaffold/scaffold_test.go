package scaffold

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"sigs.k8s.io/yaml"

	"github.com/holon-sh/cli/pkg/typegen"
)

var versions = Versions{HolonSH: "1.0.0", HolonUI: "2.0.0", HolonPy: "3.0.0"}

func TestWrite(t *testing.T) {
	for _, language := range Languages {
		t.Run(language, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "orders")
			project := Project{Dir: dir, Language: language, Name: "io.github.acme/Order_Desk", Versions: versions}
			created, _, _, err := project.Write()
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Contains(created, ".gitignore") || !slices.Contains(created, "ui/main.ts") || !slices.Contains(created, "package.json") {
				t.Fatalf("created = %v", created)
			}
			binding := `createApps({ "order-desk": { endpoint: new URL("/mcp/orders", location.origin) } })["order-desk"]`
			if language == "go" {
				binding = `bindApps(identity, contracts, { "order-desk": { endpoint: new URL("/mcp/orders", location.origin) } })["order-desk"]`
			}
			if main := read(t, dir, "ui/main.ts"); !strings.Contains(main, binding) {
				t.Errorf("ui/main.ts:\n%s", main)
			}
			var pkg struct{ Dependencies map[string]string }
			if err := json.Unmarshal([]byte(read(t, dir, "package.json")), &pkg); err != nil {
				t.Fatal(err)
			}
			want := map[string]string{"holon-ui": project.Versions.HolonUI, "holon-sh": ""}
			switch language {
			case "ts":
				want["holon-sh"] = project.Versions.HolonSH
			case "py":
				if pyproject := read(t, dir, "pyproject.toml"); !strings.Contains(pyproject, `"holon-sh==`+project.Versions.HolonPy+`"`) {
					t.Fatalf("pyproject.toml:\n%s", pyproject)
				}
			}
			for name, version := range want {
				if pkg.Dependencies[name] != version {
					t.Errorf("%s = %q, want %q", name, pkg.Dependencies[name], version)
				}
			}
			if _, _, _, err := project.Write(); err == nil || !strings.Contains(err.Error(), "orders already has") {
				t.Errorf("second Write = %v, want the files it would overwrite", err)
			}
		})
	}
}

func TestWriteBesideAProject(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "billing")
	put(t, dir, "pyproject.toml", "[project]\nname = \"billing\"\ndependencies = [\"MCP>=2.4\"]\n\n[dependency-groups]\nlint = [\"mypy\", {include-group = \"dev\"}]\n")
	put(t, dir, ".gitignore", ".venv\n")
	put(t, dir, "package.json", "{\n  \"name\": \"billing\",\n  \"scripts\": {\n    \"build\": \"tsc\"\n  },\n  \"devDependencies\": {\n    \"vite\": \"^7.0.0\"\n  }\n}\n")
	project := Project{Dir: dir, Language: "py", Name: "com.example/billing", Versions: versions}
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
	if !strings.Contains(pkg, `"vite": "^7.0.0"`) || !strings.Contains(pkg, `"build": "tsc"`) || !strings.Contains(pkg, `"holon-ui": "2.0.0"`) {
		t.Errorf("package.json:\n%s", pkg)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "build script") {
		t.Errorf("notes = %v", notes)
	}
	dependencies, dev, err := project.PythonRequirements()
	if err != nil || !slices.Equal(dependencies, []string{"holon-sh==3.0.0"}) || len(dev) != 0 {
		t.Errorf("PythonRequirements = %v, %v, %v; want holon-sh only", dependencies, dev, err)
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
		} else if body, ok := npm[r.URL.Path]; ok {
			_, _ = w.Write([]byte(body))
		} else {
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	registries := Registries{NPM: server.URL, PyPI: server.URL, Client: server.Client()}

	versions, err := Resolve(context.Background(), "py", registries)
	if err != nil {
		t.Fatal(err)
	}
	if want := (Versions{HolonUI: "0.0.1-dev.10", HolonPy: "0.0.1.dev5"}); versions != want {
		t.Errorf("py versions = %+v, want %+v", versions, want)
	}
	if _, err := Resolve(context.Background(), "ts", registries); err == nil || !strings.Contains(err.Error(), "npm holon-sh: no release reads the clients this holon writes") {
		t.Errorf("ts Resolve = %v, want no holon-sh release for this contract", err)
	}
}

func TestAddApp(t *testing.T) {
	entry := "  - name: orders\n    path: \"orders\"\n    access:\n      server: {}\n"
	for _, test := range []struct {
		name, before, after, path, err string
	}{
		{name: "new file", after: "apps:\n" + entry},
		{name: "comment in path", path: "#draft/orders", after: "apps:\n" + strings.Replace(entry, `"orders"`, `"#draft/orders"`, 1)},
		{
			name:   "colon in path",
			path:   "draft: old/orders",
			before: "apps:\n",
			after:  "apps:\n" + strings.Replace(entry, `"orders"`, `"draft: old/orders"`, 1),
		},
		{
			name:   "appends in place",
			before: "# Apps\napps:\n  - name: chat\n    path: chat\n    access:\n      server: {}",
			after:  "# Apps\napps:\n  - name: chat\n    path: chat\n    access:\n      server: {}\n" + entry,
		},
		{
			name:   "indentless list",
			before: "apps:\n- name: chat\n  path: chat\n",
			after:  "apps:\n- name: chat\n  path: chat\n" + strings.ReplaceAll(entry, "\n  ", "\n")[2:],
		},
		{name: "list not last", before: "apps:\n  - name: chat\n    path: chat\nsettings: {}\n", err: "could not add orders to the apps list"},
		{name: "duplicate", before: "apps:\n  - name: orders\n    path: orders\n", err: "already has an app named orders"},
	} {
		t.Run(test.name, func(t *testing.T) {
			manifest := filepath.Join(t.TempDir(), "holon.yaml")
			if test.before != "" {
				if err := os.WriteFile(manifest, []byte(test.before), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if test.path == "" {
				test.path = "orders"
			}
			added, err := AddApp(manifest, "orders", test.path)
			if err == nil {
				err = added.Write()
			}
			if test.err != "" {
				if err == nil || !strings.Contains(err.Error(), test.err) {
					t.Fatalf("AddApp = %v, want %q", err, test.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(manifest)
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(test.after, string(data)); diff != "" {
				t.Errorf("holon.yaml (-want +got):\n%s", diff)
			}
			var project struct{ Apps []struct{ Name, Path string } }
			if err := yaml.Unmarshal(data, &project); err != nil {
				t.Fatal(err)
			}
			if app := project.Apps[len(project.Apps)-1]; app.Name != "orders" || app.Path != test.path {
				t.Errorf("registered app = %+v, want orders at %q", app, test.path)
			}
		})
	}
}
