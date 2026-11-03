package cloud

import "encoding/json"

func ServerJSONSchema() ([]byte, error) {
	reflector := configurationReflector()
	reflected, err := reflector.Reflect(struct {
		Meta struct {
			Publisher struct {
				Holon holonDescriptorMetadata `json:"sh.holon,omitempty"`
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
