package installation

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/holon-sh/cli/pkg/installation/installationtest"
	"github.com/holon-sh/cli/pkg/mcpregistry"
	"github.com/holon-sh/cli/pkg/typegen"

	"github.com/holon-sh/protocol"
)

func TestGoDependencyPreparation(t *testing.T) {
	h := newRunHarness(t)
	h.control.Handle([]string{protocol.ToolGetAppContract}, map[string]string{"session": "dev-session", "app": "orders"},
		func(w http.ResponseWriter, _ *http.Request) {
			installationtest.WriteJSON(w, json.RawMessage(`{"serverInfo":{"name":"example.org/orders","version":"1.0.0"},"capabilities":{"tools":{}},`+
				`"lists":{"tools/list":{"pages":[{"tools":[{"name":"get_order",`+
				`"inputSchema":{"type":"object","properties":{"id":{"type":"string","minLength":1}},"required":["id"],"additionalProperties":false},`+
				`"outputSchema":{"type":"object","properties":{"total":{"type":"number"}},"required":["total"],"additionalProperties":false}}]}]}}}`))
		})
	h.start(t)
	manager, err := NewClient(t.Context(), h.run.Auth)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	sourceFiles(t, root, map[string]string{
		"go.mod": "module example.org/caller\n\ngo 1.27.0\n\nrequire github.com/holon-sh/sdk-go v0.0.1-dev.1\n", dockerignoreFile: "holon\nholon/**\n",
		"server.go": `package main // an MCP server
import (
 "context"
 apps "example.org/caller/holon"
)
func main() {
 var _ func(context.Context, apps.OrdersGetOrderInput) (apps.OrdersGetOrderOutput, error) = apps.Orders.GetOrder
}
`,
	})
	descriptor := &mcpregistry.Descriptor{Name: "example.org/caller", Version: "1.0.0", Requires: map[string]mcpregistry.Requirement{
		"orders": {RegistryRef: protocol.RegistryRef{Name: "example.org/orders", Version: "1.0.0"}},
	}}
	manifest := typedManifest(t,
		manifestApp{Name: "orders", resolved: resolvedApp{App: protocol.App{Name: "orders", Image: "registry.example/orders@sha256:" + strings.Repeat("a", 64)}}},
		manifestApp{Name: "caller", Path: root, resolved: resolvedApp{App: protocol.App{Name: "caller", Requires: []string{"orders"}}, descriptor: descriptor}},
	)
	options := buildOptions{manager: manager, host: protocol.DevHost, session: "dev-session"}
	_, cleanupDev, err := prepareManifestTypes(t.Context(), manifest, options, map[string]*typegen.Served{})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupDev()
	clients, err := os.ReadFile(filepath.Join(root, "holon", "clients.go"))
	if err != nil || !strings.Contains(string(clients), `holon "github.com/holon-sh/sdk-go"`) || !strings.Contains(string(clients), " GetOrder(") {
		t.Fatalf("the app's Go clients in its source: %v\n%s", err, clients)
	}
}
