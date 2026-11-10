package installation

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
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
	reflected.WithID("https://holon.sh/schema/v1/project.schema.json").
		WithSchema("https://json-schema.org/draft/2020-12/schema").WithTitle("Holon project manifest")
	reflected.WithDescription("Field reference for holon.yaml. Declare at least one app. " +
		"The CLI validates source choices, app names, paths, and references.")
	return json.MarshalIndent(reflected, "", "  ")
}

func (manifestApp) Description() string {
	return "Declare exactly one source: path, image, registry, or remote."
}

// projectSchema is ProjectJSONSchema compiled, with the compiler that resolves its locations.
var projectSchema = sync.OnceValues(func() (*validator.Compiler, error) {
	generated, err := ProjectJSONSchema()
	if err != nil {
		return nil, err
	}
	document, err := validator.UnmarshalJSON(bytes.NewReader(generated))
	if err != nil {
		return nil, err
	}
	compiler := validator.NewCompiler()
	if err := compiler.AddResource(projectSchemaID, document); err != nil {
		return nil, err
	}
	_, err = compiler.Compile(projectSchemaID)
	return compiler, err
})

const projectSchemaID = "https://holon.sh/schema/v1/project.schema.json"

// validateProject checks a holon.yaml, converted to JSON, against ProjectJSONSchema and describes each
// problem by its field path, such as apps[0].registry.
func validateProject(data []byte) []string {
	compiler, err := projectSchema()
	if err != nil {
		return []string{err.Error()}
	}
	schema, err := compiler.Compile(projectSchemaID)
	if err != nil {
		return []string{err.Error()}
	}
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
		at := fieldPath(instance, e.InstanceLocation)
		switch k := e.ErrorKind.(type) {
		case *kind.AdditionalProperties:
			var allowed []string
			if parent, err := compiler.Compile(e.SchemaURL); err == nil {
				allowed = slices.Sorted(maps.Keys(parent.Properties))
			}
			problems = append(problems, fmt.Sprintf("%sunknown field %s; allowed: %s", at, strings.Join(k.Properties, ", "), cmp.Or(strings.Join(allowed, ", "), "none")))
		case *kind.OneOf:
			var forms []string
			for _, cause := range e.Causes {
				for len(cause.Causes) == 1 {
					cause = cause.Causes[0]
				}
				forms = append(forms, cause.ErrorKind.LocalizedString(printer))
			}
			problems = append(problems, at+cmp.Or(strings.Join(forms, ", or "), k.LocalizedString(printer)))
		default:
			if len(e.Causes) == 0 {
				problems = append(problems, at+e.ErrorKind.LocalizedString(printer))
			}
			for _, cause := range e.Causes {
				describe(cause)
			}
		}
	}
	describe(invalid)
	return problems
}

// fieldPath writes a JSON pointer into instance as apps[0].registry, followed by ": ".
func fieldPath(instance any, location []string) string {
	var path strings.Builder
	for _, segment := range location {
		if list, ok := instance.([]any); ok {
			index, _ := strconv.Atoi(segment)
			fmt.Fprintf(&path, "[%d]", index)
			instance = list[index]
			continue
		}
		if path.Len() != 0 {
			path.WriteByte('.')
		}
		path.WriteString(segment)
		instance = instance.(map[string]any)[segment]
	}
	if path.Len() == 0 {
		return ""
	}
	return path.String() + ": "
}
