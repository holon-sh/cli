package installation

import (
	"encoding/json"

	"github.com/swaggest/jsonschema-go"

	"github.com/holon-sh/cli/pkg/mcpregistry"
	"github.com/holon-sh/protocol"
)

func ProjectJSONSchema() ([]byte, error) {
	type secretReference mcpregistry.Value
	reflector := configurationReflector()
	reflector.AddTypeMapping(mcpregistry.Value{}, jsonschema.OneOf("", secretReference{}))
	type oauthFields struct {
		ClientID     protocol.Template `description:"Client ID; {NAME} stands for a configuration input." json:"clientID" required:"true"`
		ClientSecret secretReference   `json:"clientSecret,omitempty"`
	}
	reflector.AddTypeMapping(manifestOAuth{}, oauthFields{})
	reflected, err := reflector.Reflect(manifestSpec{}, jsonschema.InterceptNullability(func(p jsonschema.InterceptNullabilityParams) {
		p.Schema.RemoveType(jsonschema.Null)
	}))
	if err != nil {
		return nil, err
	}
	reflected.WithID("https://holon.sh/schema/v1/project.schema.json").
		WithSchema("https://json-schema.org/draft/2020-12/schema").WithTitle("Holon project manifest")
	reflected.WithDescription("Field reference for holon.yaml. Declare at least one app. " +
		"The CLI validates source choices, app names, paths, and references.")
	return json.MarshalIndent(reflected, "", "  ")
}

func (manifestApp) Description() string {
	return "Declare exactly one source: path, image, registry, or remote."
}
