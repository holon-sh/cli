package mcptype

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// shape is one schema's type with the named types from its definitions and $defs sections, and
// the types to declare with it: as parsed, the recursive types it mentions; once shared, every type
// rendered by name that its binding uses first here.
type shape struct {
	value    typ
	named    map[string]typ
	declared []*declaration
}

// declarations returns the declarations the shape introduces that declares accepts.
func (s shape) declarations(declares func(*declaration) bool) []*declaration {
	var result []*declaration
	for _, ref := range s.declared {
		if declares(ref) {
			result = append(result, ref)
		}
	}
	return result
}

// nameDeclarations names each declaration of a binding's tools that declares accepts after its
// contents, so that a name never depends on which tools share it. Inputs and outputs are named
// apart, each as the binding's name, Input or Output, and the declaration's label. Declarations with
// one label take it in the order of their keys, the second and later ones with a number.
func nameDeclarations(names map[string]bool, binding string, tools catalog, declares func(*declaration) bool) {
	for _, direction := range []string{"Input", "Output"} {
		var refs []*declaration
		for _, name := range sortedKeys(tools) {
			declared := tools[name].input
			if direction == "Output" {
				declared = tools[name].output
			}
			refs = append(refs, declared.declarations(declares)...)
		}
		slices.SortFunc(refs, func(a, b *declaration) int { return cmp.Or(cmp.Compare(a.label, b.label), cmp.Compare(a.key, b.key)) })
		for _, ref := range refs {
			ref.name = unique(names, typeIdentifier(binding)+direction+typeIdentifier(ref.label))
		}
	}
}

// declaration is a type rendered by name: one that refers to itself, which only a declared name can
// express, a type that a definition or title names, or an object that several places in a binding's
// inputs or outputs share.
type declaration struct {
	value  typ
	cyclic bool
	// definition is the name of the definition or title it is declared from, if any.
	definition string
	// Once shared, label names the type by its contents within its binding's inputs or outputs, and
	// key spells them.
	label, key string
	name       string // Assigned by nameDeclarations.
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
	kindDeclared
	kindFile // A file argument, which the runtime sends as the FileInput its schema spells.
)

// typ is the type a schema describes.
type typ struct {
	kind       typeKind
	literal    Value            // kindLiteral
	item       *typ             // kindArray
	prefix     []typ            // kindTuple
	min        int              // kindTuple: required prefix length
	fields     map[string]field // kindObject
	definition string           // The definition or title that names it, until shared
	extra      extra            // kindTuple rest, kindObject additional properties
	items      []typ            // kindUnion, kindIntersection
	ref        *declaration     // kindDeclared
}

type extraKind uint8

// extraKind is what a schema says of properties or items beyond the named ones.
const (
	extraImplicit extraKind = iota // Nothing: a tuple takes any rest, and an object is free-form.
	extraClosed                    // None.
	extraOpen                      // A schema for them.
)

type extra struct {
	kind      extraKind
	value     *typ // extraOpen
	recursive bool // extraOpen: value mentions a recursive type
}

type field struct {
	value    typ
	required bool
}

// objectType is the type of an object schema. One that names fields without declaring a schema for
// others is closed, since the generated types take the named fields to be all there are: a
// misspelled argument fails excess-property checks rather than reaching a server that ignores it,
// and a result keeps its fields under Omit, which an index signature would reduce to unknown. An
// implicit object therefore names no fields.
func objectType(fields map[string]field, extra extra) typ {
	if len(fields) > 0 && extra.kind == extraImplicit {
		extra.kind = extraClosed
	}
	return typ{kind: kindObject, fields: fields, extra: extra}
}

// parseShape parses a schema. With files, an object schema marked x-mcp-file is a file argument.
func parseShape(ctx context.Context, schema Value, files bool) (shape, error) {
	parser := &parser{ctx: ctx, root: schema, files: files, references: map[string]*declaration{}}
	value, err := parser.reference("#", schema, 0)
	if err != nil {
		return shape{}, err
	}
	result := shape{value: value, named: map[string]typ{}}
	root, _ := schema.(Object)
	for _, section := range []string{"definitions", "$defs"} {
		definitions, _ := root[section].(Object)
		for _, name := range sortedKeys(definitions) {
			pointer := "#/" + section + "/" + strings.NewReplacer("~", "~0", "/", "~1").Replace(name)
			if result.named[name], err = parser.reference(pointer, definitions[name], 0); err != nil {
				return shape{}, err
			}
		}
	}
	for _, reference := range sortedKeys(parser.references) {
		result.declared = append(result.declared, parser.references[reference])
	}
	return result, nil
}

type parser struct {
	ctx        context.Context
	root       Value
	files      bool
	references map[string]*declaration // Being parsed, or found to be recursive.
	mentions   int                     // Times a recursive type has been named.
}

// resolve finds the schema a reference points at.
func (p *parser) resolve(reference string) (Value, error) {
	path, ok := strings.CutPrefix(reference, "#")
	if !ok {
		return nil, fmt.Errorf("external schema reference is unsupported: %s", reference)
	}
	target, ok := pointer(p.root, path)
	if !ok {
		return nil, fmt.Errorf("unresolved schema reference: %s", reference)
	}
	return target, nil
}

// reference returns the type a reference resolves to. Its target is inlined unless it reaches
// itself, in which case it is declared once and every mention of it names that declaration.
func (p *parser) reference(reference string, target Value, depth int) (typ, error) {
	if known, ok := p.references[reference]; ok {
		known.cyclic = true
		p.mentions++
		return typ{kind: kindDeclared, ref: known}, nil
	}
	if p.selfDefined(target, reference, map[string]bool{}) {
		return typ{kind: kindUnknown}, nil
	}
	if alias(target) {
		return p.parse(target, depth) // The schema it names is declared instead, if that one recurs.
	}
	current := &declaration{definition: definitionName(reference)}
	p.references[reference] = current
	value, err := p.parse(target, depth)
	if err != nil {
		return typ{}, err
	}
	if value.kind != kindDeclared {
		value.definition = cmp.Or(current.definition, value.definition)
	}
	if !current.cyclic {
		delete(p.references, reference)
		return value, nil
	}
	current.definition, value.definition = value.definition, ""
	current.value = value
	return typ{kind: kindDeclared, ref: current}, nil
}

// definitionName is the name a reference gives its target: its key in the root's definitions or
// $defs section, if it is one.
func definitionName(reference string) string {
	for _, section := range []string{"#/definitions/", "#/$defs/"} {
		if name, ok := strings.CutPrefix(reference, section); ok && !strings.Contains(name, "/") {
			return strings.NewReplacer("~1", "/", "~0", "~").Replace(name)
		}
	}
	return ""
}

// selfDefined reports whether schema reaches reference through other references and combinators
// alone. With no array or object in between, as in `type T = T | string`, no language can declare it.
func (p *parser) selfDefined(schema Value, reference string, seen map[string]bool) bool {
	object, _ := schema.(Object)
	if next, ok := object["$ref"].(string); ok {
		if next == reference {
			return true
		}
		if seen[next] {
			return false
		}
		seen[next] = true
		target, err := p.resolve(next)
		return err == nil && p.selfDefined(target, reference, seen) // parse reports the error.
	}
	for _, combinator := range combinators {
		if items, ok := object[combinator.key].([]Value); ok {
			return slices.ContainsFunc(items, func(item Value) bool { return p.selfDefined(item, reference, seen) })
		}
	}
	return false
}

// alias reports whether schema only names another schema: a reference, or a combinator of one.
func alias(schema Value) bool {
	object, _ := schema.(Object)
	if _, ok := object["$ref"].(string); ok {
		return true
	}
	for _, combinator := range combinators {
		if items, ok := object[combinator.key].([]Value); ok {
			return len(items) == 1 && alias(items[0])
		}
	}
	return false
}

var combinators = []struct {
	key  string
	kind typeKind
}{{"anyOf", kindUnion}, {"oneOf", kindUnion}, {"allOf", kindIntersection}}

func (p *parser) extra(value Value, present bool, depth int) (extra, error) {
	if !present {
		return extra{kind: extraImplicit}, nil
	}
	if value == false {
		return extra{kind: extraClosed}, nil
	}
	before := p.mentions
	item, err := p.parse(value, depth+1)
	if err != nil {
		return extra{}, err
	}
	return extra{kind: extraOpen, value: &item, recursive: p.mentions > before}, nil
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
	// The marker makes the schema a file however it spells the FileInput shape, by reference too.
	if _, marked := object["x-mcp-file"]; marked && p.files {
		return typ{kind: kindFile}, nil
	}
	if reference, ok := object["$ref"].(string); ok {
		target, err := p.resolve(reference)
		if err != nil {
			return typ{}, err
		}
		return p.reference(reference, target, depth+1)
	}
	for _, combinator := range combinators {
		if items, ok := object[combinator.key].([]Value); ok {
			types, err := p.parseAll(items, depth)
			if err != nil {
				return typ{}, err
			}
			if len(types) == 1 && types[0].kind == kindDeclared {
				return types[0], nil
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
		value := objectType(fields, extra)
		// A title names an object only if it names fields: tools title every property, and the type
		// of a free-form one is not worth a name.
		if title, ok := object["title"].(string); ok && len(fields) > 0 {
			value.definition = title
		}
		return value, nil
	}
	return typ{kind: kindUnknown}, nil
}
