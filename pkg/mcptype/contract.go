package mcptype

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/opencontainers/go-digest"
)

// The v1 contract document. Decoding a value validates it; there is no separate validate step.

// Identity names a published app.
type Identity struct {
	Name, Version string
}

// Tool is one tool's schemas. Output is nil when the tool declares no outputSchema.
type Tool struct {
	Input  Object
	Output Object
}

// Resource is one resource's contents schema.
type Resource struct {
	Output Object
}

// Contract holds tools keyed by name and resources keyed by URI. The wire format is v1's arrays, written in key order.
type Contract struct {
	Tools     map[string]Tool
	Resources map[string]Resource
}

// Metadata is the document an image publishes.
type Metadata struct {
	Identity Identity
	Schema   *Contract
	Requires map[string]string
}

func decodeIdentity(value Value) (Identity, error) {
	object, err := asObject(value)
	if err != nil {
		return Identity{}, err
	}
	var identity Identity
	for name, target := range map[string]*string{"name": &identity.Name, "version": &identity.Version} {
		raw, err := requiredField(object, name)
		if err != nil {
			return Identity{}, err
		}
		if *target, err = asString(raw); err != nil {
			return Identity{}, err
		}
		if *target == "" {
			return Identity{}, errors.New("identity name and version must be nonempty")
		}
	}
	return identity, nil
}

func decodeContract(value Value) (Contract, error) {
	object, err := asObject(value)
	if err != nil {
		return Contract{}, err
	}
	raw, err := requiredField(object, "tools")
	if err != nil {
		return Contract{}, err
	}
	tools, err := asArray(raw)
	if err != nil {
		return Contract{}, err
	}
	contract := Contract{Tools: map[string]Tool{}, Resources: map[string]Resource{}}
	for _, item := range tools {
		entry, err := asObject(item)
		if err != nil {
			return Contract{}, err
		}
		raw, err := requiredField(entry, "name")
		if err != nil {
			return Contract{}, err
		}
		name, err := asString(raw)
		if err != nil {
			return Contract{}, err
		}
		if raw, err = requiredField(entry, "inputSchema"); err != nil {
			return Contract{}, err
		}
		tool := Tool{}
		if tool.Input, err = asObject(raw); err != nil {
			return Contract{}, err
		}
		// "outputSchema": null is rejected rather than read as absent.
		if raw, ok := entry["outputSchema"]; ok {
			if tool.Output, err = asObject(raw); err != nil {
				return Contract{}, err
			}
		}
		if tool.Input["type"] != "object" {
			return Contract{}, fmt.Errorf("%s: inputSchema must have type object", name)
		}
		if _, exists := contract.Tools[name]; name == "" || exists {
			return Contract{}, errors.New("tool names must be nonempty and unique")
		}
		contract.Tools[name] = tool
	}
	if raw, ok := object["resources"]; ok {
		resources, err := asArray(raw)
		if err != nil {
			return Contract{}, err
		}
		for _, item := range resources {
			entry, err := asObject(item)
			if err != nil {
				return Contract{}, err
			}
			raw, err := requiredField(entry, "uri")
			if err != nil {
				return Contract{}, err
			}
			uri, err := asString(raw)
			if err != nil {
				return Contract{}, err
			}
			if raw, err = requiredField(entry, "outputSchema"); err != nil {
				return Contract{}, err
			}
			resource := Resource{}
			if resource.Output, err = asObject(raw); err != nil {
				return Contract{}, err
			}
			if _, exists := contract.Resources[uri]; uri == "" || exists {
				return Contract{}, errors.New("resource URIs must be nonempty and unique")
			}
			contract.Resources[uri] = resource
		}
	}
	return contract, nil
}

func decodeMetadata(value Value) (Metadata, error) {
	object, err := asObject(value)
	if err != nil {
		return Metadata{}, err
	}
	for key := range object {
		if key != "identity" && key != "schema" && key != "requires" {
			return Metadata{}, fmt.Errorf("unknown metadata field %q", key)
		}
	}
	metadata := Metadata{Requires: map[string]string{}}
	raw, err := requiredField(object, "identity")
	if err != nil {
		return Metadata{}, err
	}
	if metadata.Identity, err = decodeIdentity(raw); err != nil {
		return Metadata{}, err
	}
	if raw, ok := object["schema"]; ok {
		contract, err := decodeContract(raw)
		if err != nil {
			return Metadata{}, err
		}
		metadata.Schema = &contract
	}
	if raw, ok := object["requires"]; ok {
		requires, err := asObject(raw)
		if err != nil {
			return Metadata{}, err
		}
		for alias, item := range requires {
			if alias == "" {
				return Metadata{}, errors.New("dependency aliases must be nonempty")
			}
			value, err := asString(item)
			if err != nil {
				return Metadata{}, err
			}
			d, err := digest.Parse(value)
			if err != nil || d.Algorithm() != digest.SHA256 {
				return Metadata{}, fmt.Errorf("dependency %s requires a SHA-256 digest", alias)
			}
			metadata.Requires[alias] = value
		}
	}
	return metadata, nil
}

// toValue is the contract's wire form as a Value, whose keys serialize sorted.
func (c Contract) toValue() Object {
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
	return wire
}

func writeContracts(out *bytes.Buffer, contracts map[string]Contract) {
	values := make(Object, len(contracts))
	for alias, contract := range contracts {
		values[alias] = contract.toValue()
	}
	writeValue(out, values)
}
