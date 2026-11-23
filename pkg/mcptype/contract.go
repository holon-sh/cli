package mcptype

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/holon-sh/cli/pkg/mcpcontract"
	"github.com/holon-sh/protocol"
	"github.com/opencontainers/go-digest"
)

// contractTool is the schemas of one tool, resource or event. A resource has no Input, and Output is
// nil when a tool declares no outputSchema.
type contractTool struct{ Input, Output Object }

type contract struct{ Tools, Resources, Events map[string]contractTool }

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

// decodeTool reads a tool whose name is a valid MCP name and whose [MCP Tool] inputSchema is an object of type
// object.
//
// [MCP Tool]: https://modelcontextprotocol.io/specification/2026-07-28/schema#tool
func decodeTool(tool mcpcontract.Tool) (contractTool, error) {
	if !protocol.ValidMCPName(tool.Name) {
		return contractTool{}, fmt.Errorf("tool name %q is not a valid MCP name", tool.Name)
	}
	input, err := decodeSchema("inputSchema", tool.Input)
	if err != nil {
		return contractTool{}, fmt.Errorf("%s: %w", tool.Name, err)
	}
	decoded := contractTool{Input: input}
	if len(tool.Output) != 0 {
		if decoded.Output, err = decodeSchema("outputSchema", tool.Output); err != nil {
			return contractTool{}, fmt.Errorf("%s: %w", tool.Name, err)
		}
	}
	if input["type"] != "object" {
		return contractTool{}, fmt.Errorf("%s: inputSchema must have type object", tool.Name)
	}
	return decoded, nil
}

func CheckTool(tool mcpcontract.Tool) error {
	_, err := decodeTool(tool)
	return err
}

func decodeContract(source mcpcontract.Contract) (contract, error) {
	result := contract{Tools: map[string]contractTool{}, Resources: map[string]contractTool{}, Events: map[string]contractTool{}}
	for _, tool := range source.Tools {
		decoded, err := decodeTool(tool)
		if err != nil {
			return contract{}, err
		}
		if _, exists := result.Tools[tool.Name]; exists {
			return contract{}, fmt.Errorf("tool %s is listed twice", tool.Name)
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

// MarshalJSON writes the contract as a runtime reads it: tools, resources and events sorted by name or URI.
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
	return json.Marshal(wire)
}
