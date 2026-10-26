package mcptype

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/opencontainers/go-digest"
)

// The v1 contract document. Decoding a value validates it; there is no separate validate step.

// contractIdentity names a published app.
type contractIdentity struct {
	Name, Version string
}

// contractTool is one tool's schemas. Output is nil when the tool declares no outputSchema.
type contractTool struct {
	Input  Object
	Output Object
}

// contractResource is one resource's contents schema.
type contractResource struct {
	Output Object
}

// contract holds tools keyed by name and resources keyed by URI. The wire format is v1's arrays, written in key order.
type contract struct {
	Tools     map[string]contractTool
	Resources map[string]contractResource
}

// metadata is the document an image publishes.
type metadata struct {
	Identity contractIdentity
	Schema   *contract
	Requires map[string]string
}

func decodeIdentity(value Value) (contractIdentity, error) {
	object, err := asObject(value)
	if err != nil {
		return contractIdentity{}, err
	}
	var identity contractIdentity
	for name, target := range map[string]*string{"name": &identity.Name, "version": &identity.Version} {
		raw, err := requiredField(object, name)
		if err != nil {
			return contractIdentity{}, err
		}
		if *target, err = asString(raw); err != nil {
			return contractIdentity{}, err
		}
		if *target == "" {
			return contractIdentity{}, errors.New("identity name and version must be nonempty")
		}
	}
	return identity, nil
}

func decodeContract(value Value) (contract, error) {
	object, err := asObject(value)
	if err != nil {
		return contract{}, err
	}
	raw, err := requiredField(object, "tools")
	if err != nil {
		return contract{}, err
	}
	tools, err := asArray(raw)
	if err != nil {
		return contract{}, err
	}
	result := contract{Tools: map[string]contractTool{}, Resources: map[string]contractResource{}}
	for _, item := range tools {
		entry, err := asObject(item)
		if err != nil {
			return contract{}, err
		}
		raw, err := requiredField(entry, "name")
		if err != nil {
			return contract{}, err
		}
		name, err := asString(raw)
		if err != nil {
			return contract{}, err
		}
		if raw, err = requiredField(entry, "inputSchema"); err != nil {
			return contract{}, err
		}
		tool := contractTool{}
		if tool.Input, err = asObject(raw); err != nil {
			return contract{}, err
		}
		// "outputSchema": null is rejected rather than read as absent.
		if raw, ok := entry["outputSchema"]; ok {
			if tool.Output, err = asObject(raw); err != nil {
				return contract{}, err
			}
		}
		if tool.Input["type"] != "object" {
			return contract{}, fmt.Errorf("%s: inputSchema must have type object", name)
		}
		if _, exists := result.Tools[name]; name == "" || exists {
			return contract{}, errors.New("tool names must be nonempty and unique")
		}
		result.Tools[name] = tool
	}
	if raw, ok := object["resources"]; ok {
		resources, err := asArray(raw)
		if err != nil {
			return contract{}, err
		}
		for _, item := range resources {
			entry, err := asObject(item)
			if err != nil {
				return contract{}, err
			}
			raw, err := requiredField(entry, "uri")
			if err != nil {
				return contract{}, err
			}
			uri, err := asString(raw)
			if err != nil {
				return contract{}, err
			}
			if raw, err = requiredField(entry, "outputSchema"); err != nil {
				return contract{}, err
			}
			resource := contractResource{}
			if resource.Output, err = asObject(raw); err != nil {
				return contract{}, err
			}
			if _, exists := result.Resources[uri]; uri == "" || exists {
				return contract{}, errors.New("resource URIs must be nonempty and unique")
			}
			result.Resources[uri] = resource
		}
	}
	return result, nil
}

func decodeMetadata(value Value) (metadata, error) {
	object, err := asObject(value)
	if err != nil {
		return metadata{}, err
	}
	for key := range object {
		if key != "identity" && key != "schema" && key != "requires" {
			return metadata{}, fmt.Errorf("unknown metadata field %q", key)
		}
	}
	result := metadata{Requires: map[string]string{}}
	raw, err := requiredField(object, "identity")
	if err != nil {
		return metadata{}, err
	}
	if result.Identity, err = decodeIdentity(raw); err != nil {
		return metadata{}, err
	}
	if raw, ok := object["schema"]; ok {
		contract, err := decodeContract(raw)
		if err != nil {
			return metadata{}, err
		}
		result.Schema = &contract
	}
	if raw, ok := object["requires"]; ok {
		requires, err := asObject(raw)
		if err != nil {
			return metadata{}, err
		}
		for alias, item := range requires {
			if alias == "" {
				return metadata{}, errors.New("dependency aliases must be nonempty")
			}
			value, err := asString(item)
			if err != nil {
				return metadata{}, err
			}
			d, err := digest.Parse(value)
			if err != nil || d.Algorithm() != digest.SHA256 {
				return metadata{}, fmt.Errorf("dependency %s requires a SHA-256 digest", alias)
			}
			result.Requires[alias] = value
		}
	}
	return result, nil
}

// MarshalJSON writes the contract with tools and resources sorted by name or URI.
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
	return json.Marshal(wire)
}
