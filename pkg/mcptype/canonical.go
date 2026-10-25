package mcptype

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"slices"
)

var (
	annotations  = []string{"description", "title", "$comment", "examples"}
	unordered    = []string{"required", "enum", "type", "allOf", "anyOf", "oneOf"}
	schemaMaps   = []string{"properties", "$defs", "definitions", "patternProperties", "dependentSchemas", "dependencies"}
	schemaValues = []string{"additionalProperties", "unevaluatedProperties", "propertyNames", "contains", "items", "prefixItems", "additionalItems", "unevaluatedItems", "not", "if", "then", "else", "contentSchema"}
)

// canonical drops annotations and sorts order-insensitive keywords, recursively.
func canonical(schema Object) Object {
	clone := cloneObject(schema)
	normalizeObject(clone)
	return clone
}

// canonicalContract is the contract with every schema in canonical form.
func canonicalContract(contract Contract) Contract {
	result := Contract{Tools: make(map[string]Tool, len(contract.Tools)), Resources: make(map[string]Resource, len(contract.Resources))}
	for name, tool := range contract.Tools {
		canonicalTool := Tool{Input: canonical(tool.Input)}
		if tool.Output != nil {
			canonicalTool.Output = canonical(tool.Output)
		}
		result.Tools[name] = canonicalTool
	}
	for uri, resource := range contract.Resources {
		result.Resources[uri] = Resource{Output: canonical(resource.Output)}
	}
	return result
}

// digest is "sha256:<hex>" of the canonical contract's JSON with sorted keys and no whitespace.
func digest(contract Contract) string {
	return hash(canonicalContract(contract).toValue())
}

func hash(value Value) string {
	sum := sha256.Sum256(encode(value))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func normalize(value Value) {
	switch value := value.(type) {
	case Object:
		normalizeObject(value)
	case []Value:
		for _, item := range value {
			normalize(item)
		}
	}
}

func normalizeObject(object Object) {
	for _, key := range annotations {
		delete(object, key)
	}
	for key, item := range object {
		switch {
		case slices.Contains(unordered, key):
			items, ok := item.([]Value)
			if !ok {
				continue
			}
			if key != "enum" {
				for _, item := range items {
					normalize(item)
				}
			}
			encoded := make(map[int][]byte, len(items))
			indices := make([]int, len(items))
			for i := range items {
				encoded[i] = encode(items[i])
				indices[i] = i
			}
			slices.SortStableFunc(indices, func(a, b int) int { return bytes.Compare(encoded[a], encoded[b]) })
			sorted := make([]Value, len(items))
			for i, index := range indices {
				sorted[i] = items[index]
			}
			copy(items, sorted)
		case slices.Contains(schemaMaps, key):
			if schemas, ok := item.(Object); ok {
				for _, schema := range schemas {
					normalize(schema)
				}
			}
		case slices.Contains(schemaValues, key):
			normalize(item)
		}
	}
}
