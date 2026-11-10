package installation

import (
	"encoding/json"

	"github.com/swaggest/jsonschema-go"

	"github.com/holon-sh/cli/pkg/mcpregistry"
	"github.com/holon-sh/protocol"
)

func ServerJSONSchema() ([]byte, error) {
	type endpointRequirement struct {
		URL string `description:"URL of a running MCP server." format:"uri" json:"url" required:"true"`
	}
	reflector := configurationReflector()
	reflector.AddTypeMapping(mcpregistry.Requirement{}, jsonschema.OneOf(struct{}{}, protocol.RegistryRef{}, endpointRequirement{}))
	reflected, err := reflector.Reflect(struct {
		Meta struct {
			Publisher struct {
				Holon mcpregistry.HolonMetadata `json:"sh.holon,omitempty"`
			} `json:"io.modelcontextprotocol.registry/publisher-provided,omitempty"`
		} `json:"_meta,omitempty"`
	}{})
	if err != nil {
		return nil, err
	}
	reflected.WithID("https://holon.sh/schema/v1/server.schema.json").WithSchema("https://json-schema.org/draft/2020-12/schema").WithTitle("Holon server metadata")
	reflected.WithDescription("Holon publisher metadata in server.json. Standard descriptor fields are defined by the MCP Registry specification.")
	return json.MarshalIndent(reflected, "", "  ")
}
