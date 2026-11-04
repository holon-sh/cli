package installation

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/holon-sh/cli/pkg/mcpcontract"
	"github.com/holon-sh/cli/pkg/mcpregistry"

	"github.com/holon-sh/protocol"
)

func TestGoDependencyPreparation(t *testing.T) {
	fixture, registry := buildRegistry(t)
	name := strings.TrimPrefix(registry.URL, "https://") + "/orders"
	options := buildOptions{HTTPClient: registry.Client(), Env: []string{"DOCKER_CONFIG=" + t.TempDir()}, Stderr: io.Discard}
	metadata := &mcpcontract.Metadata{
		Identity: mcpcontract.Identity{Name: "example.org/orders", Version: "1.0.0"},
		Schema: &mcpcontract.Contract{Tools: []mcpcontract.Tool{{
			Name:   "get_order",
			Input:  json.RawMessage(`{"type":"object","properties":{"id":{"type":"string","minLength":1}},"required":["id"],"additionalProperties":false}`),
			Output: json.RawMessage(`{"type":"object","properties":{"total":{"type":"number"}},"required":["total"],"additionalProperties":false}`),
		}}},
	}
	image := publishTypeMetadata(t, fixture, name, metadata, options, nil)
	root := t.TempDir()
	const module = "module example.org/caller\n\ngo 1.27.0\n\n" +
		"require github.com/holon-sh/sdk/go v0.0.0-20261025144240-b6f8fd7fc600\n"
	sourceFiles(t, root, map[string]string{
		"go.mod": module, dockerignoreFile: "holon\nholon/**\n",
		"server.go": `package main
import (
 "context"
 apps "example.org/caller/holon"
)
func main() {
 var _ func(context.Context, apps.OrdersGetOrderInput) (apps.OrdersGetOrderOutput, error) = apps.Orders.GetOrder
}
`,
	})
	descriptor := &mcpregistry.Descriptor{Name: "example.org/caller", Version: "1.0.0", Requires: map[string]protocol.RegistryRef{
		"orders": {Name: metadata.Identity.Name, Version: metadata.Identity.Version},
	}}
	manifest := typedManifest(t,
		manifestApp{Name: "orders", resolved: resolvedApp{App: protocol.App{Name: "orders", Image: image}}},
		manifestApp{Name: "caller", Path: root, resolved: resolvedApp{App: protocol.App{Name: "caller", Requires: []string{"orders"}}, descriptor: descriptor}},
	)
	workspace := new(typeWorkspace)
	if err := workspace.acquire(manifest); err != nil {
		t.Fatal(err)
	}
	defer workspace.close()
	options.types = workspace
	prepared, cleanup, err := prepareManifestTypes(t.Context(), manifest, options)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	p := prepared.Apps[1].resolved.types
	if _, err := os.Stat(filepath.Join(root, "holon", "clients.go")); err != nil {
		t.Fatal("the Go client package was not written into the app for its editor")
	}
	if _, err := stageTypes(p, options.check); err != nil {
		t.Fatal(err)
	}
	unchanged, _ := os.ReadFile(filepath.Join(root, "go.mod"))
	if string(unchanged) != module {
		t.Fatal("generation or import changed module dependencies")
	}
}
