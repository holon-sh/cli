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

func TestWrite(t *testing.T) {
	for _, language := range Languages {
		t.Run(language, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "orders")
			project := Project{Dir: dir, Language: language, Name: "io.github.acme/Order_Desk", Versions: Versions{HolonSH: "1.0.0", HolonUI: "2.0.0", HolonPy: "3.0.0"}}
			files, err := project.Write()
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Contains(files, ".gitignore") || !slices.Contains(files, "ui/main.ts") {
				t.Fatalf("files = %v", files)
			}
			main, err := os.ReadFile(filepath.Join(dir, "ui", "main.ts"))
			if err != nil {
				t.Fatal(err)
			}
			// A Go server has no generated client, so its view binds the runtime's untyped one.
			binding := `createApps({ "order-desk": { endpoint: new URL("/mcp/orders", location.origin) } })["order-desk"]`
			if language == "go" {
				binding = `bindApps(identity, contracts, { "order-desk": { endpoint: new URL("/mcp/orders", location.origin) } })["order-desk"]`
			}
			if !strings.Contains(string(main), binding) {
				t.Errorf("ui/main.ts:\n%s", main)
			}
			data, err := os.ReadFile(filepath.Join(dir, "package.json"))
			if err != nil {
				t.Fatal(err)
			}
			var pkg struct{ Dependencies map[string]string }
			if err := json.Unmarshal(data, &pkg); err != nil {
				t.Fatal(err)
			}
			want := map[string]string{"holon-ui": project.Versions.HolonUI, "holon-sh": ""}
			switch language {
			case "ts":
				want["holon-sh"] = project.Versions.HolonSH
			case "py":
				data, err := os.ReadFile(filepath.Join(dir, "pyproject.toml"))
				if err != nil || !strings.Contains(string(data), `"holon-sh==`+project.Versions.HolonPy+`"`) {
					t.Fatalf("pyproject.toml = %s, %v", data, err)
				}
			}
			for name, version := range want {
				if pkg.Dependencies[name] != version {
					t.Errorf("%s = %q, want %q", name, pkg.Dependencies[name], version)
				}
			}
			if _, err := project.Write(); err == nil || !strings.Contains(err.Error(), "is not empty") {
				t.Errorf("second Write = %v, want a not-empty error", err)
			}
		})
	}
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

func TestRegister(t *testing.T) {
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
			err := Register(manifest, "orders", test.path)
			if test.err != "" {
				if err == nil || !strings.Contains(err.Error(), test.err) {
					t.Fatalf("Register = %v, want %q", err, test.err)
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
