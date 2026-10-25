package typegen

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureDependenciesRecognizesNodeRuntimeDeclarations(t *testing.T) {
	for _, section := range []string{"dependencies", "devDependencies", "peerDependencies"} {
		for _, pkg := range []string{"holon-sh", "holon-ui"} {
			t.Run(section+"/"+pkg, func(t *testing.T) {
				root := t.TempDir()
				put(t, root, "package.json", fmt.Sprintf(`{"%s":{"%s":"^0.0.1"}}`, section, pkg))
				var output bytes.Buffer
				if err := ensureDependencies(t.Context(), App{Name: "app", Path: root}, nil, Options{NoInstall: true, Stdout: &output}); err != nil {
					t.Fatal(err)
				}
				if output.Len() != 0 {
					t.Fatalf("existing runtime triggered another install: %s", &output)
				}
			})
		}
	}
}

func TestEnsureDependenciesAddsImportedRuntimes(t *testing.T) {
	for _, test := range []struct{ name, packageJSON, source, want, wantError string }{
		{"neither", `{}`, `export const x = 1;`, "app: npm install holon-sh\n", ""},
		{"imports holon-ui", `{}`, `import { createApps } from "holon-ui";`, "app: npm install holon-ui\n", ""},
		{"imports both", `{}`, "import { apps } from 'holon-sh';\nimport type { Inputs } from \"holon-ui\";", "app: npm install holon-ui holon-sh\n", ""},
		{"declares holon-sh, imports holon-ui", `{"dependencies":{"holon-sh":"^0.0.1"}}`, `import { createApps } from "holon-ui/runtime";`, "app: npm install holon-ui\n", ""},
		{"declares holon-ui only", `{"dependencies":{"holon-ui":"^0.0.1"}}`, `const example = "import 'holon-sh'"; // import 'holon-sh/browser'`, "", ""},
		{"legacy browser", `{}`, `import { createApps } from "holon-sh/browser";`, "", "moved to the holon-ui package"},
		{"legacy browser runtime", `{}`, `import "holon-sh/browser-runtime";`, "", "moved to the holon-ui package"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "app")
			put(t, root, "package.json", test.packageJSON)
			put(t, root, "src/view.tsx", test.source)
			// Installed dependencies and generated directories never select a runtime.
			put(t, root, "node_modules/other/index.js", `import "holon-sh/browser";`)
			var output bytes.Buffer
			app := App{Name: "app", Path: root}
			o := Options{NoInstall: true, Stdout: &output}
			usage, err := inspectClients(t.Context(), Manifest{Apps: []App{app}}, o)
			if err != nil {
				t.Fatal(err)
			}
			err = ensureDependencies(t.Context(), app, usage[app.Name].Imports, o)
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("err = %v, want %q", err, test.wantError)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if output.String() != test.want {
				t.Fatalf("output = %q, want %q", &output, test.want)
			}
		})
	}
}
