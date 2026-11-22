package mcptype

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// catalog is one binding's compiled tools.
type catalog map[string]compiledTool

type compiledTool struct {
	input, output shape
	structured    bool
}

func parseCatalog(ctx context.Context, entries map[string]contractTool, files bool, declares func(typ) bool) (catalog, error) {
	names := sortedKeys(entries)
	tools := make([]compiledTool, len(names))
	inputs, outputs := make([]*shape, len(names)), make([]*shape, len(names))
	for i, name := range names {
		tool := entries[name]
		input, err := parseShape(ctx, tool.Input, files)
		if err != nil {
			return catalog{}, err
		}
		output := shape{named: map[string]typ{}}
		if tool.Output != nil {
			if output, err = parseShape(ctx, tool.Output, false); err != nil {
				return catalog{}, err
			}
		}
		tools[i] = compiledTool{input: input, output: output, structured: tool.Output != nil}
		inputs[i], outputs[i] = &tools[i].input, &tools[i].output
	}
	// Inputs and outputs are named apart, so each shares only with its own kind.
	share(inputs, declares)
	share(outputs, declares)
	result := make(catalog, len(names))
	for i, name := range names {
		result[name] = tools[i]
	}
	return result, nil
}

// share declares each type that a definition or title names, each object that several places in
// shapes have in common and declares accepts, and each recursive type, once however many shapes
// mention it. Every other type stays where it is used.
func share(shapes []*shape, declares func(typ) bool) {
	s := &sharing{shapes: shapes, declares: declares, types: map[string]*sharedType{}, keys: map[*declaration]string{}}
	for i, shape := range shapes {
		s.current = i
		s.count(shape.value, false)
		for _, name := range sortedKeys(shape.named) {
			s.count(shape.named[name], true)
		}
	}
	for _, shape := range shapes {
		recursive := shape.declared
		shape.declared = nil
		// Recursive types come first, in the order they were parsed.
		for _, ref := range recursive {
			s.attach(s.types[s.recursiveKey(ref)])
		}
		for _, ref := range recursive {
			s.reference(s.types[s.recursiveKey(ref)])
		}
		shape.value = s.rewrite(shape.value)
		named := make(map[string]typ, len(shape.named))
		for _, name := range sortedKeys(shape.named) {
			named[name] = s.rewrite(shape.named[name])
		}
		shape.named = named
	}
}

type sharing struct {
	shapes   []*shape
	declares func(typ) bool
	current  int                    // The shape being counted.
	types    map[string]*sharedType // By structure.
	keys     map[*declaration]string
}

// sharedType is a recursive or named type, or an object that might be shared.
type sharedType struct {
	declaration *declaration
	source      typ // The type as parsed, rewritten when first referenced.
	shape       int // The first shape that mentions it, which declares it.
	// Mentions, counting a type's contents once. A tool's definitions only repeat its own types
	// to index them, so they share a type only with other tools.
	uses, values int
	several      bool // Whether other shapes mention it.
	attached     bool
	built        bool
}

// declared reports whether the type is declared rather than written where it is used.
func (t *sharedType) declared() bool {
	return t.declaration.cyclic || t.declaration.definition != "" || t.values > 1 || t.uses > 1 && t.several
}

// candidate reports whether a type that is not recursive might be declared.
func candidate(value typ) bool {
	return value.definition != "" || value.kind == kindObject && len(value.fields) > 0
}

// count records the uses of value and the types it contains. A shape's named definitions are not
// values.
func (s *sharing) count(value typ, named bool) {
	if value.kind == kindDeclared {
		if !s.mention(s.recursiveKey(value.ref), value.ref.value, value.ref.definition, true, named) {
			return
		}
		value = value.ref.value
	} else if candidate(value) && !s.mention(s.key(value), value, value.definition, false, named) {
		return
	}
	for _, item := range value.items {
		s.count(item, false)
	}
	for _, name := range sortedKeys(value.fields) {
		s.count(value.fields[name].value, false)
	}
	if value.extra.value != nil {
		s.count(*value.extra.value, false)
	}
}

// mention records a use of the type with key and reports whether it is the first.
func (s *sharing) mention(key string, source typ, definition string, cyclic, named bool) bool {
	shared, seen := s.types[key]
	if !seen {
		label := definition
		if label == "" {
			label = structuralLabel(source)
		}
		shared = &sharedType{declaration: &declaration{cyclic: cyclic, definition: definition, label: label, key: key}, source: source, shape: s.current}
		s.types[key] = shared
	}
	shared.several = shared.several || shared.shape != s.current
	shared.uses++
	if !named {
		shared.values++
	}
	return !seen
}

// structuralLabel names an object that no definition or title names after its first three fields in
// key order, then Etc if it has more. A field is named by the definition or title that names its
// type, or else by its own name.
func structuralLabel(value typ) string {
	var label strings.Builder
	for i, name := range sortedKeys(value.fields) {
		if i == 3 {
			label.WriteString("Etc")
			break
		}
		member := value.fields[name].value
		switch {
		case member.kind == kindDeclared && member.ref.definition != "":
			name = member.ref.definition
		case member.definition != "":
			name = member.definition
		}
		label.WriteString(typeIdentifier(name))
	}
	return label.String()
}

// rewrite copies value, naming each shared type it contains.
func (s *sharing) rewrite(value typ) typ {
	if value.kind == kindDeclared {
		return s.reference(s.types[s.recursiveKey(value.ref)])
	}
	if candidate(value) {
		if shared := s.types[s.key(value)]; shared.declared() && (value.definition != "" || s.declares(value)) {
			return s.reference(shared)
		}
	}
	return s.rewriteWithin(value)
}

func (s *sharing) rewriteWithin(value typ) typ {
	value.definition = ""
	value.items = slices.Clone(value.items)
	for i, item := range value.items {
		value.items[i] = s.rewrite(item)
	}
	value.fields = maps.Clone(value.fields)
	for _, name := range sortedKeys(value.fields) {
		member := value.fields[name]
		value.fields[name] = field{value: s.rewrite(member.value), required: member.required}
	}
	if value.extra.value != nil {
		item := s.rewrite(*value.extra.value)
		value.extra.value = &item
	}
	return value
}

// attach adds a shared type to the declarations of the shape that first mentions it.
func (s *sharing) attach(shared *sharedType) {
	if !shared.attached {
		shared.attached = true
		shape := s.shapes[shared.shape]
		shape.declared = append(shape.declared, shared.declaration)
	}
}

func (s *sharing) reference(shared *sharedType) typ {
	s.attach(shared)
	if !shared.built {
		shared.built = true
		shared.declaration.value = s.rewriteWithin(shared.source)
	}
	return typ{kind: kindDeclared, ref: shared.declaration}
}

func (s *sharing) recursiveKey(ref *declaration) string {
	key, ok := s.keys[ref]
	if !ok {
		key = s.key(typ{kind: kindDeclared, ref: ref})
		s.keys[ref] = key
	}
	return key
}

// key spells value so that types spelled alike are equal.
func (s *sharing) key(value typ) string {
	var out strings.Builder
	spell(&out, value, map[*declaration]int{})
	return out.String()
}

// spell writes value with the names its definitions and titles give it, so that types named apart
// stay apart. A recursive type is spelled out where it is first reached and numbered after that,
// which spells equal types alike since everything is visited in order.
func spell(out *strings.Builder, value typ, seen map[*declaration]int) {
	fmt.Fprintf(out, "(%d%s", value.kind, quoted(value.definition))
	defer out.WriteByte(')')
	switch value.kind {
	case kindDeclared:
		if number, ok := seen[value.ref]; ok {
			fmt.Fprintf(out, "#%d", number)
			return
		}
		seen[value.ref] = len(seen)
		out.WriteString(quoted(value.ref.definition))
		spell(out, value.ref.value, seen)
	case kindLiteral:
		out.Write(encode(value.literal))
	case kindTuple:
		fmt.Fprintf(out, "%d", value.min)
	}
	for _, item := range value.items {
		spell(out, item, seen)
	}
	for _, name := range sortedKeys(value.fields) {
		member := value.fields[name]
		fmt.Fprintf(out, "%s%t", quoted(name), member.required)
		spell(out, member.value, seen)
	}
	if value.kind == kindTuple || value.kind == kindObject {
		fmt.Fprintf(out, "%d%t", value.extra.kind, value.extra.recursive)
		if value.extra.value != nil {
			spell(out, *value.extra.value, seen)
		}
	}
}

func compileCatalogs(ctx context.Context, request generateRequest, entries func(contract) map[string]contractTool, files bool) (map[string]catalog, error) {
	catalogs := make(map[string]catalog, len(request.contracts))
	for _, name := range sortedKeys(request.contracts) {
		compiled, err := parseCatalog(ctx, entries(request.contracts[name]), files, declaresShared[request.Target])
		if err != nil {
			return nil, err
		}
		catalogs[name] = compiled
	}
	return catalogs, nil
}
