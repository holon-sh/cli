package installation

import (
	"path/filepath"
	"testing"

	"github.com/holon-sh/cli/pkg/mcpregistry"
)

// typedManifest writes the server descriptors typegen reads for in-memory test apps.
func typedManifest(t *testing.T, apps ...manifestApp) manifestSpec {
	t.Helper()
	for _, app := range apps {
		if d := app.resolved.descriptor; d != nil && app.Path != "" {
			writeDescriptor(t, filepath.Join(app.Path, "server.json"), map[string]any{
				"name": d.Name, "version": d.Version, "description": app.Name,
				"_meta": map[string]any{mcpregistry.PublisherMetadata: map[string]any{"sh.holon": map[string]any{"requires": d.Requires}}},
			})
		}
	}
	return manifestSpec{Apps: apps}
}
