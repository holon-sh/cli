package mcptype

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/holon-sh/cli/pkg/mcpcontract"
	"github.com/opencontainers/go-digest"
)

// contractTool is one tool's schemas. Output is nil when the tool declares no outputSchema.
type contractTool struct {
	Input  Object
	Output Object
}

type contractEvent struct {
	Input   Object
	Payload Object
}

type contract struct {
	Tools     map[string]contractTool
	Resources map[string]Object
	Events    map[string]contractEvent
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
	value, err := decodeJSON(raw)
	if err != nil {
		return nil, err
	}
	return asObject(value)
}

func decodeContract(source mcpcontract.Contract) (contract, error) {
	result := contract{Tools: map[string]contractTool{}, Resources: map[string]Object{}, Events: map[string]contractEvent{}}
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
		result.Resources[resource.URI] = output
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
		result.Events[event.Name] = contractEvent{Input: input, Payload: payload}
	}
	return result, nil
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

// MarshalJSON writes the contract with tools, resources and events sorted by name or URI.
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
			resources = append(resources, Object{"uri": uri, "outputSchema": c.Resources[uri]})
		}
		wire["resources"] = resources
	}
	if len(c.Events) > 0 {
		events := make([]Value, 0, len(c.Events))
		for _, name := range sortedKeys(c.Events) {
			events = append(events, Object{"name": name, "inputSchema": c.Events[name].Input, "payloadSchema": c.Events[name].Payload})
		}
		wire["events"] = events
	}
	return json.Marshal(wire)
}
