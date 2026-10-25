package mcptype

import (
	"bytes"
	"errors"
	"fmt"
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

// Expectation is the contract a caller was built against.
type Expectation struct {
	Identity Identity
	Contract Contract
}

// Metadata is the document an image publishes.
type Metadata struct {
	Version  uint32
	Identity Identity
	Contract *Contract
	Requires map[string]Expectation
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

func decodeSchema(value Value) (Object, error) {
	return asObject(value)
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
		if tool.Input, err = decodeSchema(raw); err != nil {
			return Contract{}, err
		}
		// "outputSchema": null is rejected rather than read as absent.
		if raw, ok := entry["outputSchema"]; ok {
			if tool.Output, err = decodeSchema(raw); err != nil {
				return Contract{}, err
			}
		}
		if !valueEqual(tool.Input["type"], "object") {
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
			if resource.Output, err = decodeSchema(raw); err != nil {
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

func decodeExpectation(value Value) (Expectation, error) {
	object, err := asObject(value)
	if err != nil {
		return Expectation{}, err
	}
	var expectation Expectation
	raw, err := requiredField(object, "identity")
	if err != nil {
		return Expectation{}, err
	}
	if expectation.Identity, err = decodeIdentity(raw); err != nil {
		return Expectation{}, err
	}
	if raw, err = requiredField(object, "contract"); err != nil {
		return Expectation{}, err
	}
	expectation.Contract, err = decodeContract(raw)
	return expectation, err
}

func decodeMetadata(value Value) (Metadata, error) {
	object, err := asObject(value)
	if err != nil {
		return Metadata{}, err
	}
	raw, err := requiredField(object, "version")
	if err != nil {
		return Metadata{}, err
	}
	version, ok := raw.(Number)
	if !ok {
		return Metadata{}, fmt.Errorf("invalid type: %s, expected u32", describe(raw))
	}
	unsigned, ok := version.asU64()
	if !ok || unsigned > 1<<32-1 {
		return Metadata{}, fmt.Errorf("invalid value: %s, expected u32", describe(raw))
	}
	if unsigned != 1 {
		return Metadata{}, fmt.Errorf("unsupported metadata version %d", unsigned)
	}
	metadata := Metadata{Version: 1, Requires: map[string]Expectation{}}
	if raw, err = requiredField(object, "identity"); err != nil {
		return Metadata{}, err
	}
	if metadata.Identity, err = decodeIdentity(raw); err != nil {
		return Metadata{}, err
	}
	if raw, ok := object["contract"]; ok && raw != nil {
		contract, err := decodeContract(raw)
		if err != nil {
			return Metadata{}, err
		}
		metadata.Contract = &contract
	}
	if raw, ok := object["requires"]; ok {
		requires, err := asObject(raw)
		if err != nil {
			return Metadata{}, err
		}
		for alias, item := range requires {
			if metadata.Requires[alias], err = decodeExpectation(item); err != nil {
				return Metadata{}, err
			}
		}
		if _, ok := metadata.Requires[""]; ok {
			return Metadata{}, errors.New("dependency aliases must be nonempty")
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

// writeWire is serde's struct serialization of the contract: fields in declaration order
// (name, inputSchema, outputSchema; tools, resources), schemas with sorted keys.
func (c Contract) writeWire(out *bytes.Buffer) {
	out.WriteString(`{"tools":[`)
	for i, name := range sortedKeys(c.Tools) {
		if i > 0 {
			out.WriteByte(',')
		}
		tool := c.Tools[name]
		out.WriteString(`{"name":`)
		writeQuoted(out, name)
		out.WriteString(`,"inputSchema":`)
		writeValue(out, tool.Input)
		if tool.Output != nil {
			out.WriteString(`,"outputSchema":`)
			writeValue(out, tool.Output)
		}
		out.WriteByte('}')
	}
	out.WriteByte(']')
	if len(c.Resources) > 0 {
		out.WriteString(`,"resources":[`)
		for i, uri := range sortedKeys(c.Resources) {
			if i > 0 {
				out.WriteByte(',')
			}
			out.WriteString(`{"uri":`)
			writeQuoted(out, uri)
			out.WriteString(`,"outputSchema":`)
			writeValue(out, c.Resources[uri].Output)
			out.WriteByte('}')
		}
		out.WriteByte(']')
	}
	out.WriteByte('}')
}

func writeContracts(out *bytes.Buffer, contracts map[string]Contract) {
	out.WriteByte('{')
	for i, alias := range sortedKeys(contracts) {
		if i > 0 {
			out.WriteByte(',')
		}
		writeQuoted(out, alias)
		out.WriteByte(':')
		contracts[alias].writeWire(out)
	}
	out.WriteByte('}')
}

func (i Identity) writeWire(out *bytes.Buffer) {
	out.WriteString(`{"name":`)
	writeQuoted(out, i.Name)
	out.WriteString(`,"version":`)
	writeQuoted(out, i.Version)
	out.WriteByte('}')
}
