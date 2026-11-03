package cloud

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"
)

// typedManifest writes the server descriptors typegen reads for in-memory test apps.
func typedManifest(t *testing.T, apps ...manifestApp) manifestSpec {
	t.Helper()
	for _, app := range apps {
		if d := app.resolved.descriptor; d != nil && app.Path != "" {
			data, err := json.Marshal(map[string]any{
				"name": d.Name, "version": d.Version, "description": app.Name,
				"_meta": map[string]any{publisherMetadata: map[string]any{"sh.holon": map[string]any{"requires": d.Requires}}},
			})
			if err != nil {
				t.Fatal(err)
			}
			sourceFiles(t, app.Path, map[string]string{"server.json": string(data)})
		}
	}
	return manifestSpec{Apps: apps}
}

func installRuntime(t *testing.T, root string) {
	t.Helper()
	sourceFiles(t, filepath.Join(root, "node_modules", nodeRuntime), map[string]string{
		"package.json": `{"name":"holon-sh","holon":{"slot":"generated","contractVersion":1}}`,
	})
}

func TestGeneratedClientsAreAvailableDuringPostinstall(t *testing.T) {
	root := t.TempDir()
	sourceFiles(t, root, map[string]string{
		"package.json":          `{"type":"module","scripts":{"postinstall":"node check.mjs"}}`,
		".holon-types/stage.sh": stageRuntimeSlots,
		"check.mjs": `import {tool as node} from "holon-sh";
import {tool as browser} from "holon-ui";
if (node !== "lookup" || browser !== "lookup") throw Error("generated clients unavailable during postinstall");`,
	})
	for _, name := range []string{nodeRuntime, uiRuntime} {
		sourceFiles(t, filepath.Join(root, "node_modules", name), map[string]string{
			"package.json":       `{"type":"module","exports":"./generated/index.js"}`,
			"generated/index.js": `export const tool = "placeholder";`,
		})
		sourceFiles(t, filepath.Join(root, ".holon-types", name), map[string]string{
			"generated/index.js": `export const tool = "lookup";`,
		})
	}
	if err := stageNodePackage(root); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), "npm", "run", "--offline", "postinstall")
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("app postinstall: %v\n%s", err, output)
	}
}
