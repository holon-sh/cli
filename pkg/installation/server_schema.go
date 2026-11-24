package installation

import (
	"encoding/json"

	"github.com/swaggest/jsonschema-go"

	"github.com/holon-sh/cli/pkg/mcpregistry"
	"github.com/holon-sh/protocol"
)

func ServerJSONSchema() ([]byte, error) {
	type Delegation struct {
		MayAct bool `description:"Whether the required app may act for this server: a call it makes as this server counts as this server's own, for a person who lets this server act for them." json:"mayAct,omitempty"`
	}
	type registryRequirement struct {
		protocol.RegistryRef
		Delegation
	}
	type endpointRequirement struct {
		URL string `description:"URL of a running MCP server." format:"uri" json:"url" required:"true"`
		Delegation
	}
	reflector := configurationReflector()
	reflector.AddTypeMapping(mcpregistry.Requirement{}, jsonschema.OneOf(Delegation{}, registryRequirement{}, endpointRequirement{}))
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
