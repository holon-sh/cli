package typegen

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeSelection(t *testing.T) {
	for _, test := range []struct{ name, packageJSON, source, want, wantError string }{
		{"neither", `{}`, `export const x = 1;`, "app: npm install holon-sh\n", ""},
		{"imports holon-ui", `{}`, `import { createApps } from "holon-ui";`, "app: npm install holon-ui\n", ""},
		{"imports both", `{}`, "import { apps } from 'holon-sh';\nimport type { Inputs } from \"holon-ui\";", "app: npm install holon-ui holon-sh\n", ""},
		{"declares holon-sh, imports holon-ui", `{"dependencies":{"holon-sh":"^0.0.1"}}`, `import { createApps } from "holon-ui/runtime";`, "app: npm install holon-ui\n", ""},
		{"declares holon-ui only", `{"dependencies":{"holon-ui":"^0.0.1"}}`, `const example = "import 'holon-sh'"; // import 'holon-sh/browser'`, "", ""},
		{"dependencies", `{"dependencies":{"holon-ui":"^0.0.1","holon-sh":"^0.0.1"}}`, `import 'holon-ui'; import 'holon-sh';`, "", ""},
		{"devDependencies", `{"devDependencies":{"holon-ui":"^0.0.1","holon-sh":"^0.0.1"}}`, `import 'holon-ui'; import 'holon-sh';`, "", ""},
		{"peerDependencies", `{"peerDependencies":{"holon-ui":"^0.0.1","holon-sh":"^0.0.1"}}`, `import 'holon-ui'; import 'holon-sh';`, "", ""},
		{"legacy browser", `{}`, `import { createApps } from "holon-sh/browser";`, "", "moved to the holon-ui package"},
		{"legacy browser runtime", `{}`, `import "holon-sh/browser-runtime";`, "", "moved to the holon-ui package"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "app")
			put(t, root, "package.json", test.packageJSON)
			put(t, root, "src/view.tsx", test.source)
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
