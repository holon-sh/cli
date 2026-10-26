package mcptype

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
)

// Value is a JSON value: nil, bool, Number, string, []Value or Object.
// Objects are unordered and serialize with sorted keys.
type Value = any

// Object is a JSON object.
type Object map[string]Value

type numberKind uint8

const (
	posInt numberKind = iota
	negInt
	float
)

// Number keeps serde_json's three representations apart: 1 and 1.0 are different values.
type Number struct {
	kind numberKind
	u    uint64
	i    int64
	f    float64
}

func (n Number) isInteger() bool { return n.kind != float }

// String formats the number without losing integer precision or the integer/float distinction.
func (n Number) String() string {
	switch n.kind {
	case posInt:
		return strconv.FormatUint(n.u, 10)
	case negInt:
		return strconv.FormatInt(n.i, 10)
	}
	text := strconv.FormatFloat(n.f, 'g', -1, 64)
	if !strings.ContainsAny(text, ".eE") {
		text += ".0"
	}
	return text
}

// parseNumber classifies a JSON number literal as serde_json does: integers that fit u64 or i64
// stay integers, "-0" and everything else become floats.
func parseNumber(literal string) (Number, error) {
	if !strings.ContainsAny(literal, ".eE") {
		if strings.HasPrefix(literal, "-") {
			if value, err := strconv.ParseInt(literal, 10, 64); err == nil && value != 0 {
				return Number{kind: negInt, i: value}, nil
			}
		} else if value, err := strconv.ParseUint(literal, 10, 64); err == nil {
			return Number{kind: posInt, u: value}, nil
		}
	}
	value, err := strconv.ParseFloat(literal, 64)
	if math.IsInf(value, 0) || (err != nil && !errors.Is(err, strconv.ErrRange)) {
		return Number{}, errors.New("number out of range")
	}
	return Number{kind: float, f: value}, nil
}

// decodeJSON parses one JSON document.
func decodeJSON(data []byte) (Value, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var raw any
	if err := decoder.Decode(&raw); err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, errors.New("trailing characters")
	}
	return fromAny(raw)
}

// fromAny normalizes the private tree produced by json.Decoder.UseNumber.
func fromAny(raw any) (Value, error) {
	switch raw := raw.(type) {
	case json.Number:
		return parseNumber(string(raw))
	case []any:
		for i, item := range raw {
			value, err := fromAny(item)
			if err != nil {
				return nil, err
			}
			raw[i] = value
		}
		return raw, nil
	case map[string]any:
		for key, item := range raw {
			value, err := fromAny(item)
			if err != nil {
				return nil, err
			}
			raw[key] = value
		}
		return Object(raw), nil
	}
	return raw, nil // null, booleans and strings already have their final representation.
}

// encode writes compact JSON with sorted object keys using the standard encoder.
func encode(value Value) []byte {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err) // Values come from validated JSON or the compiler's own wire types.
	}
	return data
}

func quoted(text string) string { return string(encode(text)) }

// MarshalJSON preserves the number representation used for schema comparison.
func (n Number) MarshalJSON() ([]byte, error) { return []byte(n.String()), nil }

func sortedKeys[V any](m map[string]V) []string {
	return slices.Sorted(maps.Keys(m))
}

// describe names a value in serde's "invalid type" errors.
func describe(value Value) string {
	switch value := value.(type) {
	case nil:
		return "null"
	case bool:
		return fmt.Sprintf("boolean `%t`", value)
	case Number:
		if value.isInteger() {
			return fmt.Sprintf("integer `%s`", value)
		}
		return fmt.Sprintf("floating point `%s`", value)
	case string:
		return fmt.Sprintf("string %s", quoted(value))
	case []Value:
		return "sequence"
	}
	return "map"
}

func asObject(value Value) (Object, error) {
	if object, ok := value.(Object); ok {
		return object, nil
	}
	return nil, fmt.Errorf("invalid type: %s, expected a map", describe(value))
}

func asArray(value Value) ([]Value, error) {
	if items, ok := value.([]Value); ok {
		return items, nil
	}
	return nil, fmt.Errorf("invalid type: %s, expected a sequence", describe(value))
}

func asString(value Value) (string, error) {
	if text, ok := value.(string); ok {
		return text, nil
	}
	return "", fmt.Errorf("invalid type: %s, expected a string", describe(value))
}

// requiredField returns a required struct field.
func requiredField(object Object, name string) (Value, error) {
	value, ok := object[name]
	if !ok {
		return nil, fmt.Errorf("missing field `%s`", name)
	}
	return value, nil
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
		token = strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
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
