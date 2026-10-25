package typegen

import (
	"bytes"
	"fmt"
	"testing"
)

func TestEnsureDependenciesRecognizesNodeRuntimeDeclarations(t *testing.T) {
	for _, section := range []string{"dependencies", "devDependencies", "peerDependencies"} {
		t.Run(section, func(t *testing.T) {
			root := t.TempDir()
			put(t, root, "package.json", fmt.Sprintf(`{"%s":{"%s":"^0.0.1"}}`, section, nodePackage))
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
