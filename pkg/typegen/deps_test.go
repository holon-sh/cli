package typegen

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestEnsureDependenciesRecognizesNodeRuntimeDeclarations(t *testing.T) {
	for _, section := range []string{"dependencies", "devDependencies", "peerDependencies"} {
		for _, name := range []string{"@holon/apps", "@holon-sh/apps"} {
			t.Run(section+"/"+name, func(t *testing.T) {
				root := t.TempDir()
				pkg := fmt.Sprintf(`{"%s":{"%s":"^0.1.0"}}`, section, name)
				put(t, root, "package.json", pkg)
				var output bytes.Buffer
				if err := ensureDependencies(t.Context(), App{Name: "app", Path: root}, Options{NoInstall: true, Stdout: &output}); err != nil {
					t.Fatal(err)
				}
				if output.Len() != 0 {
					t.Fatalf("existing runtime triggered another install: %s", &output)
				}
				if read(t, root, "package.json") != pkg {
					t.Fatal("changed an existing runtime declaration")
				}
			})
		}
	}
}

func TestEnsureDependenciesPlansMissingRuntime(t *testing.T) {
	for _, test := range []struct {
		name, pkg, lock, want string
	}{
		{"npm default", `{}`, "", "npm install @holon/apps"},
		{"explicit pnpm", `{"packageManager":"pnpm@10.0.0"}`, "yarn.lock", "pnpm add @holon/apps"},
		{"yarn lock", `{}`, "yarn.lock", "yarn add @holon/apps"},
		{"bun lock", `{}`, "bun.lock", "bun add @holon/apps"},
		{"uv project", "", "pyproject.toml", "uv add holon-sh"},
		{"pip requirements", "", "requirements.txt", "pip install holon-sh"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			if test.pkg != "" {
				put(t, root, "package.json", test.pkg)
			} else {
				put(t, root, "server.py", "pass\n")
			}
			if test.lock != "" {
				put(t, root, test.lock, "")
			}
			var output bytes.Buffer
			if err := ensureDependencies(t.Context(), App{Name: "app", Path: root}, Options{NoInstall: true, Stdout: &output}); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(output.String(), test.want) {
				t.Fatalf("install plan = %q, want %q", output.String(), test.want)
			}
		})
	}
}
