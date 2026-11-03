package cloud

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/holon-sh/cli/pkg/protocol"
)

func descriptorDocument(name string) map[string]any {
	return map[string]any{
		"$schema": "https://static.modelcontextprotocol.io/schemas/2025-12-11/server.schema.json",
		"name":    "io.github.acme/" + name, "version": "1.4.2", "description": "Example MCP server",
		"packages": []any{map[string]any{
			"registryType": "oci", "identifier": "ghcr.io/acme/" + name + "@sha256:" + strings.Repeat("a", 64),
			"transport": map[string]any{"type": "streamable-http", "url": "http://localhost:3000/mcp"},
		}},
	}
}

func descriptorRegistry(t *testing.T, document map[string]any) *protocol.RegistrySource {
	t.Helper()
	catalog := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"server": document})
	}))
	t.Cleanup(catalog.Close)
	return &protocol.RegistrySource{RegistryRef: protocol.RegistryRef{
		Name: document["name"].(string), Version: document["version"].(string), Registry: catalog.URL,
	}}
}
