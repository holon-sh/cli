package typegen

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"testing"

	"github.com/holon-sh/cli/pkg/mcpcontract"
)

type unavailableRegistry struct{ t *testing.T }

func (r unavailableRegistry) RoundTrip(request *http.Request) (*http.Response, error) {
	r.t.Errorf("provider contract discovery requested a dependency registry: %s", request.URL)
	return nil, errors.New("dependency registry unavailable")
}

func TestContractExtractsProviderWithoutResolvingDependenciesOrChangingFiles(t *testing.T) {
	if _, err := typeLanguages["python"].localProgram(context.Background()); err != nil {
		t.Skip(err)
	}
	app := t.TempDir()
	reference := Reference{Name: "example.com/unavailable", Version: "1.0.0", Registry: "https://registry.unavailable.example"}
	files := map[string]string{
		"server.py": `import holon.apps
from holon.apps import apps
from mcp.server import MCPServer
server = MCPServer("serverInfo may differ from registry identity")
@server.tool()
def read(id: str) -> str:
    raise RuntimeError("contract extraction must not execute the provider")
`,
		"server.json": string(mustJSON(t, map[string]any{
			"name": "example.com/provider", "version": "2.0.0", "description": "Provider",
			"_meta": map[string]any{PublisherMetadata: map[string]any{"sh.holon": map[string]any{"requires": map[string]Reference{"unavailable": reference}}}},
		})),
		"holon/apps/__init__.py": "# " + mcpcontract.Marker + " app=provider\nnot valid Python ???\n",
	}
	for name, content := range files {
		put(t, app, name, content)
	}
	manifest, _, err := Directories([]string{app})
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := Contract(context.Background(), manifest, "provider", Options{HTTPClient: &http.Client{Transport: unavailableRegistry{t}}})
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Identity != (mcpcontract.Identity{Name: "example.com/provider", Version: "2.0.0"}) {
		t.Fatalf("provider identity lost: %+v", metadata.Identity)
	}
	if metadata.Contract == nil || len(metadata.Contract.Tools) != 1 || metadata.Contract.Tools[0].Name != "read" {
		t.Fatalf("own contract not extracted: %+v", metadata.Contract)
	}
	if len(metadata.Requires) != 0 {
		t.Fatalf("provider document contains caller expectations: %+v", metadata.Requires)
	}
	actual := fileMap(t, app)
	if !maps.Equal(files, actual) {
		t.Fatalf("contract discovery changed provider files: before %v; after %v", files, actual)
	}
}
