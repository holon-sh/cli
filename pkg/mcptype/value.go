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

// Value is a JSON value with serde_json's semantics: nil, bool, Number, string, []Value or Object.
// Objects are unordered and written with sorted keys, as serde_json's default map does.
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

// String formats the number as serde_json writes it: integers in decimal, floats in ryu's shortest form.
func (n Number) String() string {
	switch n.kind {
	case posInt:
		return strconv.FormatUint(n.u, 10)
	case negInt:
		return strconv.FormatInt(n.i, 10)
	}
	return formatFloat(n.f)
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

// formatFloat is ryu's pretty printing of a finite f64, which serde_json uses for floats.
func formatFloat(value float64) string {
	mantissa, exponent, _ := strings.Cut(strconv.FormatFloat(value, 'e', -1, 64), "e")
	power, _ := strconv.Atoi(exponent)
	// Serde uses fixed notation for decimal exponents -5 through 15.
	if power >= -5 && power < 16 {
		fixed := strconv.FormatFloat(value, 'f', -1, 64)
		if !strings.Contains(fixed, ".") {
			fixed += ".0"
		}
		return fixed
	}
	return mantissa + "e" + fmt.Sprintf("%+d", power)
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

// encode writes compact JSON with object keys sorted, as serde_json serializes a Value.
func encode(value Value) []byte {
	var out bytes.Buffer
	writeValue(&out, value)
	return out.Bytes()
}

func writeValue(out *bytes.Buffer, value Value) {
	switch value := value.(type) {
	case nil:
		out.WriteString("null")
	case bool:
		out.WriteString(strconv.FormatBool(value))
	case Number:
		out.WriteString(value.String())
	case string:
		writeQuoted(out, value)
	case []Value:
		out.WriteByte('[')
		for i, item := range value {
			if i > 0 {
				out.WriteByte(',')
			}
			writeValue(out, item)
		}
		out.WriteByte(']')
	case Object:
		out.WriteByte('{')
		for i, key := range sortedKeys(value) {
			if i > 0 {
				out.WriteByte(',')
			}
			writeQuoted(out, key)
			out.WriteByte(':')
			writeValue(out, value[key])
		}
		out.WriteByte('}')
	default:
		panic(fmt.Sprintf("mcptype: not a JSON value: %T", value))
	}
}

// quoted is a JSON string literal as serde_json writes it: only quotes, backslashes and
// control characters are escaped, with lowercase hex.
func quoted(text string) string {
	var out bytes.Buffer
	writeQuoted(&out, text)
	return out.String()
}

func writeQuoted(out *bytes.Buffer, text string) {
	const hex = "0123456789abcdef"
	out.WriteByte('"')
	for i := 0; i < len(text); i++ {
		switch c := text[i]; c {
		case '"':
			out.WriteString(`\"`)
		case '\\':
			out.WriteString(`\\`)
		case '\b':
			out.WriteString(`\b`)
		case '\f':
			out.WriteString(`\f`)
		case '\n':
			out.WriteString(`\n`)
		case '\r':
			out.WriteString(`\r`)
		case '\t':
			out.WriteString(`\t`)
		default:
			if c < 0x20 {
				out.WriteString(`\u00`)
				out.WriteByte(hex[c>>4])
				out.WriteByte(hex[c&0xf])
			} else {
				out.WriteByte(c)
			}
		}
	}
	out.WriteByte('"')
}

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
