package mcptype

import (
	"bytes"
	"slices"
)

var (
	annotations  = []string{"description", "title", "$comment", "examples"}
	unordered    = []string{"required", "enum", "type", "allOf", "anyOf", "oneOf"}
	schemaMaps   = []string{"properties", "$defs", "definitions", "patternProperties", "dependentSchemas", "dependencies"}
	schemaValues = []string{"additionalProperties", "unevaluatedProperties", "propertyNames", "contains", "items", "prefixItems", "additionalItems", "unevaluatedItems", "not", "if", "then", "else", "contentSchema"}
)

// canonicalContract normalizes the private JSON tree decoded by each public entrypoint.
// Caller-owned input never reaches this tree.
func canonicalContract(contract contract) contract {
	for _, tool := range contract.Tools {
		normalizeObject(tool.Input)
		normalizeObject(tool.Output)
	}
	for _, resource := range contract.Resources {
		normalizeObject(resource.Output)
	}
	return contract
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
			items, _ := item.([]Value)
			if key != "enum" {
				for _, item := range items {
					normalize(item)
				}
			}
			type encodedItem struct {
				value Value
				key   []byte
			}
			ordered := make([]encodedItem, len(items))
			for i, item := range items {
				ordered[i] = encodedItem{item, encode(item)}
			}
			slices.SortStableFunc(ordered, func(a, b encodedItem) int { return bytes.Compare(a.key, b.key) })
			for i, item := range ordered {
				items[i] = item.value
			}
		case slices.Contains(schemaMaps, key):
			schemas, _ := item.(Object)
			for _, schema := range schemas {
				normalize(schema)
			}
		case slices.Contains(schemaValues, key):
			normalize(item)
		}
	}
}
