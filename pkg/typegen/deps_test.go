package typegen

import (
	"bytes"
	"fmt"
	"testing"
)

func TestEnsureDependenciesRecognizesNodeRuntimeDeclarations(t *testing.T) {
	for _, test := range []struct{ section, name string }{
		{"dependencies", "@holon/apps"},
		{"devDependencies", "@holon/apps"},
		{"peerDependencies", "@holon-sh/apps"},
	} {
		t.Run(test.section+"/"+test.name, func(t *testing.T) {
			root := t.TempDir()
			put(t, root, "package.json", fmt.Sprintf(`{"%s":{"%s":"^0.1.0"}}`, test.section, test.name))
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
