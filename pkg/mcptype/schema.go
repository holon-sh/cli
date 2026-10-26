package mcptype

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// shape is one schema's type with the named types from its definitions and $defs sections.
type shape struct {
	value typ
	named map[string]typ
}

type typeKind uint8

const (
	kindUnknown typeKind = iota
	kindNever
	kindString
	kindInteger
	kindNumber
	kindBoolean
	kindNull
	kindLiteral
	kindArray
	kindTuple
	kindObject
	kindUnion
	kindIntersection
)

// typ is the type a schema describes.
type typ struct {
	kind    typeKind
	literal Value            // kindLiteral
	item    *typ             // kindArray
	prefix  []typ            // kindTuple
	min     int              // kindTuple: required prefix length
	fields  map[string]field // kindObject
	extra   extra            // kindTuple rest, kindObject additional properties
	items   []typ            // kindUnion, kindIntersection
}

type extraKind uint8

const (
	extraImplicit extraKind = iota
	extraClosed
	extraOpen
)

type extra struct {
	kind  extraKind
	value *typ // extraOpen
}

type field struct {
	value    typ
	required bool
}

func parseShape(ctx context.Context, schema Value) (shape, error) {
	parser := &parser{ctx: ctx, root: schema, active: map[string]bool{}}
	value, err := parser.parse(schema, 0)
	if err != nil {
		return shape{}, err
	}
	result := shape{value: value, named: map[string]typ{}}
	root, _ := schema.(Object)
	for _, section := range []string{"definitions", "$defs"} {
		definitions, _ := root[section].(Object)
		for _, name := range sortedKeys(definitions) {
			if result.named[name], err = parser.parse(definitions[name], 0); err != nil {
				return shape{}, err
			}
		}
	}
	return result, nil
}

type parser struct {
	ctx    context.Context
	root   Value
	active map[string]bool
}

func (p *parser) extra(value Value, present bool, depth int) (extra, error) {
	if !present {
		return extra{kind: extraImplicit}, nil
	}
	if value == false {
		return extra{kind: extraClosed}, nil
	}
	item, err := p.parse(value, depth+1)
	if err != nil {
		return extra{}, err
	}
	return extra{kind: extraOpen, value: &item}, nil
}

func (p *parser) parseAll(items []Value, depth int) ([]typ, error) {
	types := make([]typ, len(items))
	for i, item := range items {
		value, err := p.parse(item, depth+1)
		if err != nil {
			return nil, err
		}
		types[i] = value
	}
	return types, nil
}

func (p *parser) parse(schema Value, depth int) (typ, error) {
	if err := p.ctx.Err(); err != nil {
		return typ{}, err
	}
	if depth > 128 {
		return typ{kind: kindUnknown}, nil
	}
	object, ok := schema.(Object)
	if !ok {
		switch schema {
		case false:
			return typ{kind: kindNever}, nil
		case true:
			return typ{kind: kindUnknown}, nil
		}
		return typ{}, errors.New("schema must be an object or boolean")
	}
	if reference, ok := object["$ref"].(string); ok {
		path, ok := strings.CutPrefix(reference, "#")
		if !ok {
			return typ{}, fmt.Errorf("external schema reference is unsupported: %s", reference)
		}
		target, ok := pointer(p.root, path)
		if !ok {
			return typ{}, fmt.Errorf("unresolved schema reference: %s", reference)
		}
		if p.active[reference] {
			return typ{kind: kindUnknown}, nil
		}
		p.active[reference] = true
		value, err := p.parse(target, depth+1)
		if err != nil {
			return typ{}, err
		}
		delete(p.active, reference)
		return value, nil
	}
	for _, combinator := range []struct {
		key  string
		kind typeKind
	}{{"anyOf", kindUnion}, {"oneOf", kindUnion}, {"allOf", kindIntersection}} {
		if items, ok := object[combinator.key].([]Value); ok {
			types, err := p.parseAll(items, depth)
			if err != nil {
				return typ{}, err
			}
			return typ{kind: combinator.kind, items: types}, nil
		}
	}
	if value, ok := object["const"]; ok {
		return typ{kind: kindLiteral, literal: value}, nil
	}
	if values, ok := object["enum"].([]Value); ok {
		types := make([]typ, len(values))
		for i, value := range values {
			types[i] = typ{kind: kindLiteral, literal: value}
		}
		return typ{kind: kindUnion, items: types}, nil
	}
	if kinds, ok := object["type"].([]Value); ok {
		types := make([]typ, len(kinds))
		for i, kind := range kinds {
			variant := maps.Clone(object)
			variant["type"] = kind
			value, err := p.parse(variant, depth+1)
			if err != nil {
				return typ{}, err
			}
			types[i] = value
		}
		return typ{kind: kindUnion, items: types}, nil
	}
	kind, isString := object["type"].(string)
	switch kind {
	case "string":
		return typ{kind: kindString}, nil
	case "integer":
		return typ{kind: kindInteger}, nil
	case "number":
		return typ{kind: kindNumber}, nil
	case "boolean":
		return typ{kind: kindBoolean}, nil
	case "null":
		return typ{kind: kindNull}, nil
	case "array":
		prefix, ok := object["prefixItems"].([]Value)
		if _, declared := object["prefixItems"]; !declared {
			prefix, ok = object["items"].([]Value)
		}
		if ok {
			items, err := p.parseAll(prefix, depth)
			if err != nil {
				return typ{}, err
			}
			restKey := "additionalItems"
			if _, declared := object["prefixItems"]; declared {
				restKey = "items"
			}
			rest, declared := object[restKey]
			extra, err := p.extra(rest, declared, depth)
			if err != nil {
				return typ{}, err
			}
			var min int
			if count, ok := object["minItems"].(Number); ok && count.kind == posInt {
				min = int(count.u)
			}
			return typ{kind: kindTuple, prefix: items, min: min, extra: extra}, nil
		}
		items, declared := object["items"]
		if !declared {
			items = true
		}
		item, err := p.parse(items, depth+1)
		if err != nil {
			return typ{}, err
		}
		return typ{kind: kindArray, item: &item}, nil
	}
	_, hasProperties := object["properties"]
	if kind == "object" || !isString && hasProperties {
		required, _ := object["required"].([]Value)
		fields := map[string]field{}
		properties, _ := object["properties"].(Object)
		for _, key := range sortedKeys(properties) {
			value, err := p.parse(properties[key], depth+1)
			if err != nil {
				return typ{}, err
			}
			fields[key] = field{value: value, required: slices.Contains(required, Value(key))}
		}
		additional, declared := object["additionalProperties"]
		extra, err := p.extra(additional, declared, depth)
		if err != nil {
			return typ{}, err
		}
		return typ{kind: kindObject, fields: fields, extra: extra}, nil
	}
	return typ{kind: kindUnknown}, nil
}
