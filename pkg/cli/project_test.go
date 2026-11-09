package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"sigs.k8s.io/yaml"
)

func TestAddApp(t *testing.T) {
	entry := "  - name: orders\n    path: \"orders\"\n    access:\n      server: {}\n"
	for _, test := range []struct {
		name, before, after, path, err string
	}{
		{name: "new file", after: "apps:\n" + entry},
		{name: "comment in path", path: "#draft/orders", after: "apps:\n" + strings.Replace(entry, `"orders"`, `"#draft/orders"`, 1)},
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
			added, err := addApp(manifest, "orders", test.path)
			if err == nil {
				err = added.write()
			}
			if test.err != "" {
				if err == nil || !strings.Contains(err.Error(), test.err) {
					t.Fatalf("addApp = %v, want %q", err, test.err)
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
