package mcptype

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"

	"github.com/holon-sh/cli/pkg/mcpcontract"
	"github.com/opencontainers/go-digest"
)

// contractTool is the schemas of one tool, resource or event. A resource has no Input, and Output is
// nil when a tool declares no outputSchema.
type contractTool struct{ Input, Output Object }

type contract struct {
	Tools, Resources, Events map[string]contractTool
	// Responses are, by tool, the schemas of the JSON that tools without an outputSchema return as
	// text, each with the definitions it reaches.
	Responses map[string]Object
}

// results are the tools, each with the schema of what its typed call returns: its outputSchema, or
// the JSON a response describes.
func (c contract) results() map[string]contractTool {
	tools := maps.Clone(c.Tools)
	for name, schema := range c.Responses {
		tools[name] = contractTool{Input: tools[name].Input, Output: schema}
	}
	return tools
}

// checkIdentity requires what names a published app.
func checkIdentity(identity mcpcontract.Identity) error {
	if identity.Name == "" || identity.Version == "" {
		return errors.New("identity name and version must be nonempty")
	}
	return nil
}

func decodeSchema(member string, raw json.RawMessage) (Object, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("missing field `%s`", member)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var object Object
	if err := decoder.Decode(&object); err != nil || object == nil {
		return nil, fmt.Errorf("%s must be an object", member)
	}
	return object, nil
}

func decodeContract(source mcpcontract.Contract) (contract, error) {
	result := contract{Tools: map[string]contractTool{}, Resources: map[string]contractTool{}, Events: map[string]contractTool{}}
	for _, tool := range source.Tools {
		input, err := decodeSchema("inputSchema", tool.Input)
		if err != nil {
			return contract{}, err
		}
		decoded := contractTool{Input: input}
		if len(tool.Output) != 0 {
			if decoded.Output, err = decodeSchema("outputSchema", tool.Output); err != nil {
				return contract{}, err
			}
		}
		if input["type"] != "object" {
			return contract{}, fmt.Errorf("%s: inputSchema must have type object", tool.Name)
		}
		if _, exists := result.Tools[tool.Name]; tool.Name == "" || exists {
			return contract{}, errors.New("tool names must be nonempty and unique")
		}
		result.Tools[tool.Name] = decoded
	}
	for _, resource := range source.Resources {
		output, err := decodeSchema("outputSchema", resource.Output)
		if err != nil {
			return contract{}, err
		}
		if _, exists := result.Resources[resource.URI]; resource.URI == "" || exists {
			return contract{}, errors.New("resource URIs must be nonempty and unique")
		}
		result.Resources[resource.URI] = contractTool{Output: output}
	}
	for _, event := range source.Events {
		input, err := decodeSchema("inputSchema", event.Input)
		if err != nil {
			return contract{}, err
		}
		payload, err := decodeSchema("payloadSchema", event.Payload)
		if err != nil {
			return contract{}, err
		}
		if input["type"] != "object" {
			return contract{}, fmt.Errorf("event %s: inputSchema must have type object", event.Name)
		}
		if _, exists := result.Events[event.Name]; event.Name == "" || exists {
			return contract{}, errors.New("event names must be nonempty and unique")
		}
		result.Events[event.Name] = contractTool{Input: input, Output: payload}
	}
	responses, err := decodeResponses(source.Responses, result.Tools)
	if err != nil {
		return contract{}, err
	}
	result.Responses = responses
	return result, nil
}

// decodeResponses splits a contract's responses into one schema per tool, each with the definitions
// it reaches.
func decodeResponses(raw json.RawMessage, tools map[string]contractTool) (map[string]Object, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	document, err := decodeSchema("responses", raw)
	if err != nil {
		return nil, err
	}
	properties, ok := document["properties"].(Object)
	if !ok {
		return nil, errors.New("responses must have properties")
	}
	definitions, _ := document["$defs"].(Object)
	responses := make(map[string]Object, len(properties))
	for name, value := range properties {
		schema, ok := value.(Object)
		if tool, exists := tools[name]; !ok || !exists || tool.Output != nil {
			return nil, fmt.Errorf("responses: %s must be an object schema of a tool without an outputSchema", name)
		}
		reached := Object{}
		reach(schema, definitions, reached)
		responses[name] = maps.Clone(schema)
		responses[name]["$defs"] = reached
	}
	return responses, nil
}

// reach adds to reached each definition that value refers to, directly or through other definitions.
func reach(value Value, definitions, reached Object) {
	switch value := value.(type) {
	case Object:
		if reference, ok := value["$ref"].(string); ok {
			if rest, ok := strings.CutPrefix(reference, "#/$defs/"); ok {
				token, _, _ := strings.Cut(rest, "/")
				name := unescape(token)
				if _, seen := reached[name]; !seen && definitions[name] != nil {
					reached[name] = definitions[name]
					reach(definitions[name], definitions, reached)
				}
			}
		}
		for _, item := range value {
			reach(item, definitions, reached)
		}
	case []Value:
		for _, item := range value {
			reach(item, definitions, reached)
		}
	}
}

func decodeMetadata(document *mcpcontract.Metadata) (*contract, error) {
	if err := checkIdentity(document.Identity); err != nil {
		return nil, err
	}
	for alias, value := range document.Requires {
		if alias == "" {
			return nil, errors.New("dependency aliases must be nonempty")
		}
		if d, err := digest.Parse(value); err != nil || d.Algorithm() != digest.SHA256 {
			return nil, fmt.Errorf("dependency %s requires a SHA-256 digest", alias)
		}
	}
	if document.Schema == nil {
		return nil, nil
	}
	decoded, err := decodeContract(*document.Schema)
	if err != nil {
		return nil, err
	}
	return &decoded, nil
}

// MarshalJSON writes the contract as a runtime reads it: tools, resources and events sorted by name or
// URI, and the names of the tools whose text is JSON in place of their responses.
func (c contract) MarshalJSON() ([]byte, error) {
	tools := make([]Value, 0, len(c.Tools))
	for _, name := range sortedKeys(c.Tools) {
		tool := c.Tools[name]
		entry := Object{"name": name, "inputSchema": tool.Input}
		if tool.Output != nil {
			entry["outputSchema"] = tool.Output
		}
		tools = append(tools, entry)
	}
	wire := Object{"tools": tools}
	if len(c.Resources) > 0 {
		resources := make([]Value, 0, len(c.Resources))
		for _, uri := range sortedKeys(c.Resources) {
			resources = append(resources, Object{"uri": uri, "outputSchema": c.Resources[uri].Output})
		}
		wire["resources"] = resources
	}
	if len(c.Events) > 0 {
		events := make([]Value, 0, len(c.Events))
		for _, name := range sortedKeys(c.Events) {
			events = append(events, Object{"name": name, "inputSchema": c.Events[name].Input, "payloadSchema": c.Events[name].Output})
		}
		wire["events"] = events
	}
	if len(c.Responses) > 0 {
		wire["responses"] = sortedKeys(c.Responses)
	}
	return json.Marshal(wire)
}
