package installation

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"

	validator "github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
	"github.com/swaggest/jsonschema-go"
	"golang.org/x/text/language"
	"golang.org/x/text/message"

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
	reflected.WithID(projectSchemaID).
		WithSchema("https://json-schema.org/draft/2020-12/schema").WithTitle("Holon project manifest")
	reflected.WithDescription("Field reference for holon.yaml. Declare at least one app. " +
		"The CLI validates source choices, app names, paths, and references.")
	return json.MarshalIndent(reflected, "", "  ")
}

func (manifestApp) Description() string {
	return "Declare exactly one source: path, image, registry, or remote."
}

var projectSchema = sync.OnceValues(func() (*validator.Compiler, *validator.Schema) {
	generated, err := ProjectJSONSchema()
	if err != nil {
		panic(err)
	}
	document, err := validator.UnmarshalJSON(bytes.NewReader(generated))
	if err != nil {
		panic(err)
	}
	compiler := validator.NewCompiler()
	if err := compiler.AddResource(projectSchemaID, document); err != nil {
		panic(err)
	}
	return compiler, compiler.MustCompile(projectSchemaID)
})

const projectSchemaID = "https://holon.sh/schema/v1/project.schema.json"

func validateProject(data []byte) []string {
	compiler, schema := projectSchema()
	instance, err := validator.UnmarshalJSON(bytes.NewReader(data))
	if err == nil {
		err = schema.Validate(instance)
	}
	var invalid *validator.ValidationError
	if err == nil {
		return nil
	} else if !errors.As(err, &invalid) {
		return []string{err.Error()}
	}
	printer := message.NewPrinter(language.English)
	var problems []string
	var describe func(*validator.ValidationError)
	describe = func(e *validator.ValidationError) {
		for _, cause := range e.Causes {
			describe(cause)
		}
		if len(e.Causes) != 0 {
			return
		}
		problem := e.ErrorKind.LocalizedString(printer)
		if unknown, ok := e.ErrorKind.(*kind.AdditionalProperties); ok {
			var allowed []string
			if parent, err := compiler.Compile(e.SchemaURL); err == nil {
				allowed = slices.Sorted(maps.Keys(parent.Properties))
			}
			problem = fmt.Sprintf("unknown field %s; allowed: %s", strings.Join(unknown.Properties, ", "), strings.Join(allowed, ", "))
		}
		if len(e.InstanceLocation) != 0 {
			problem = strings.Join(e.InstanceLocation, ".") + ": " + problem
		}
		problems = append(problems, problem)
	}
	describe(invalid)
	return problems
}
