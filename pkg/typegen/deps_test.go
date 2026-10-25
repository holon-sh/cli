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
				if err := ensureDependencies(t.Context(), App{Name: "app", Path: root}, Options{NoInstall: true, Stdout: &output}); err != nil {
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
	for _, test := range []struct{ name, packageJSON, source, want string }{
		{"neither", `{}`, `export const x = 1;`, "app: npm install holon-sh\n"},
		{"imports holon-ui", `{}`, `import { createApps } from "holon-ui";`, "app: npm install holon-ui\n"},
		{"imports both", `{}`, "import { apps } from 'holon-sh';\nimport type { Inputs } from \"holon-ui\";", "app: npm install holon-ui holon-sh\n"},
		{"declares holon-sh, imports holon-ui", `{"dependencies":{"holon-sh":"^0.0.1"}}`, `import { createApps } from "holon-ui/runtime";`, "app: npm install holon-ui\n"},
		{"declares holon-ui only", `{"dependencies":{"holon-ui":"^0.0.1"}}`, `export const x = 1;`, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "app")
			put(t, root, "package.json", test.packageJSON)
			put(t, root, "src/view.tsx", test.source)
			// Installed dependencies and generated directories never select a runtime.
			put(t, root, "node_modules/other/index.js", `import "holon-sh/browser";`)
			var output bytes.Buffer
			if err := ensureDependencies(t.Context(), App{Name: "app", Path: root}, Options{NoInstall: true, Stdout: &output}); err != nil {
				t.Fatal(err)
			}
			if output.String() != test.want {
				t.Fatalf("output = %q, want %q", &output, test.want)
			}
		})
	}
}

func TestEnsureDependenciesRejectsHolonShBrowser(t *testing.T) {
	for _, specifier := range []string{"holon-sh/browser", "holon-sh/browser-runtime"} {
		root := t.TempDir()
		put(t, root, "package.json", `{"dependencies":{"holon-sh":"^0.0.1"}}`)
		put(t, root, "ui/main.ts", `import { createApps } from "`+specifier+`";`)
		var output bytes.Buffer
		err := ensureDependencies(t.Context(), App{Name: "app", Path: root}, Options{NoInstall: true, Stdout: &output})
		const want = `app imports holon-sh/browser, which moved to the holon-ui package; depend on holon-ui and import from "holon-ui"`
		if err == nil || err.Error() != want {
			t.Fatalf("%s: err = %v, want %q", specifier, err, want)
		}
		if strings.Contains(output.String(), "install") {
			t.Fatalf("%s: installed before rejecting: %s", specifier, &output)
		}
	}
}
