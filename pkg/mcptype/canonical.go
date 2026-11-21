package mcptype

import (
	"bytes"
	"slices"
)

var (
	annotations  = []string{"description", "title", "$comment", "examples"}
	unordered    = []string{"required", "enum", "type"}
	schemaMaps   = []string{"properties", "$defs", "definitions", "patternProperties", "dependentSchemas", "dependencies"}
	schemaValues = []string{"additionalProperties", "unevaluatedProperties", "propertyNames", "contains", "items", "prefixItems", "additionalItems", "unevaluatedItems", "not", "if", "then", "else", "contentSchema", "allOf", "anyOf", "oneOf"}
)

func canonicalContract(contract contract) {
	for _, part := range parts {
		for _, entry := range part.entries(contract) {
			normalizeObject(entry.Input)
			normalizeObject(entry.Output)
		}
	}
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
