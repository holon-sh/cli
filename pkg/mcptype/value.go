package mcptype

import (
	"encoding/json"
	"maps"
	"slices"
	"strconv"
	"strings"
)

// Value is a JSON value: nil, bool, json.Number, string, []Value or Object.
// Objects are unordered and serialize with sorted keys.
type Value = any

// Object is a JSON object.
type Object = map[string]Value

// encode writes compact JSON with sorted object keys using the standard encoder.
func encode(value Value) []byte {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err) // Values come from validated JSON or the compiler's own wire types.
	}
	return data
}

func quoted(text string) string { return string(encode(text)) }

func sortedKeys[V any](m map[string]V) []string {
	return slices.Sorted(maps.Keys(m))
}

// unescape decodes a JSON pointer reference token.
func unescape(token string) string {
	return strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
}

// pointer resolves a JSON pointer as serde_json's Value::pointer does.
func pointer(root Value, path string) (Value, bool) {
	if path == "" {
		return root, true
	}
	if !strings.HasPrefix(path, "/") {
		return nil, false
	}
	target := root
	for _, token := range strings.Split(path, "/")[1:] {
		token = unescape(token)
		switch current := target.(type) {
		case Object:
			next, ok := current[token]
			if !ok {
				return nil, false
			}
			target = next
		case []Value:
			if strings.HasPrefix(token, "+") || (strings.HasPrefix(token, "0") && len(token) != 1) {
				return nil, false
			}
			index, err := strconv.Atoi(token)
			if err != nil || index < 0 || index >= len(current) {
				return nil, false
			}
			target = current[index]
		default:
			return nil, false
		}
	}
	return target, true
}
