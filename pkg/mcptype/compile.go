package mcptype

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// catalog is one binding's compiled tools.
type catalog map[string]compiledTool

type compiledTool struct {
	input, output shape
	structured    bool
}

func parseCatalog(ctx context.Context, contract contract) (catalog, error) {
	names := sortedKeys(contract.Tools)
	tools := make([]compiledTool, len(names))
	inputs, outputs := make([]*shape, len(names)), make([]*shape, len(names))
	for i, name := range names {
		tool := contract.Tools[name]
		input, err := parseShape(ctx, tool.Input)
		if err != nil {
			return catalog{}, err
		}
		output := shape{named: map[string]typ{}}
		if tool.Output != nil {
			if output, err = parseShape(ctx, tool.Output); err != nil {
				return catalog{}, err
			}
		}
		tools[i] = compiledTool{input: input, output: output, structured: tool.Output != nil}
		inputs[i], outputs[i] = &tools[i].input, &tools[i].output
	}
	// Inputs and outputs are named apart, so each shares only with its own kind.
	share(inputs)
	share(outputs)
	result := make(catalog, len(names))
	for i, name := range names {
		result[name] = tools[i]
	}
	return result, nil
}

// share declares each object that several places in shapes have in common once, and each recursive
// type once however many shapes declare it. Every other type stays where it is used.
func share(shapes []*shape) {
	s := &sharing{shapes: shapes, types: map[string]*sharedType{}, keys: map[*declaration]string{}}
	for i, shape := range shapes {
		s.current = i
		s.count(shape.value, "", false)
		for _, name := range sortedKeys(shape.named) {
			s.count(shape.named[name], typeIdentifier(name), true)
		}
	}
	for _, shared := range s.types {
		if !shared.agree || !shared.several {
			shared.declaration.definition = ""
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
	shapes  []*shape
	current int                    // The shape being counted.
	types   map[string]*sharedType // By structure.
	keys    map[*declaration]string
}

// sharedType is a recursive type, or an object that might be shared.
type sharedType struct {
	declaration *declaration
	source      typ // The type as parsed, rewritten when first referenced.
	shape       int // The first shape that mentions it, which declares it.
	// Mentions, counting a type's contents once. A tool's definitions only repeat its own types
	// to index them, so they share a type only with other tools.
	uses, values int
	several      bool // Whether other shapes mention it.
	agree        bool // Whether every mention names declaration.definition.
	attached     bool
	built        bool
}

// declared reports whether the type is declared rather than written where it is used.
func (t *sharedType) declared() bool {
	return t.declaration.cyclic || t.values > 1 || t.uses > 1 && t.several
}

// count records the uses of value and the types it contains. path names value within its tool, and
// a shape's named definitions are not values.
func (s *sharing) count(value typ, path string, named bool) {
	switch value.kind {
	case kindDeclared:
		if s.mention(s.recursiveKey(value.ref), value.ref.value, value.ref.definition, value.ref.local, true, named) {
			s.countWithin(value.ref.value, value.ref.local)
		}
		return
	case kindObject:
		if len(value.fields) > 0 {
			if value.definition != "" {
				path = typeIdentifier(value.definition)
			}
			if !s.mention(s.key(value), value, value.definition, path, false, named) {
				return
			}
		}
	}
	s.countWithin(value, path)
}

func (s *sharing) countWithin(value typ, path string) {
	switch value.kind {
	case kindArray:
		s.count(*value.item, path+"Item", false)
	case kindTuple:
		for _, item := range value.prefix {
			s.count(item, path+"Item", false)
		}
		if value.extra.value != nil {
			s.count(*value.extra.value, path+"Item", false)
		}
	case kindObject:
		for _, name := range sortedKeys(value.fields) {
			s.count(value.fields[name].value, path+typeIdentifier(name), false)
		}
		if value.extra.value != nil {
			s.count(*value.extra.value, path+"Value", false)
		}
	case kindUnion, kindIntersection:
		for _, item := range value.items {
			s.count(item, path, false)
		}
	}
}

// mention records a use of the type with key and reports whether it is the first.
func (s *sharing) mention(key string, source typ, definition, local string, cyclic, named bool) bool {
	shared, seen := s.types[key]
	if !seen {
		shared = &sharedType{declaration: &declaration{cyclic: cyclic, local: local, definition: definition}, source: source, shape: s.current, agree: true}
		s.types[key] = shared
	}
	shared.several = shared.several || shared.shape != s.current
	shared.agree = shared.agree && shared.declaration.definition == definition
	shared.uses++
	if !named {
		shared.values++
	}
	return !seen
}

// rewrite copies value, naming each shared type it contains.
func (s *sharing) rewrite(value typ) typ {
	switch value.kind {
	case kindDeclared:
		return s.reference(s.types[s.recursiveKey(value.ref)])
	case kindObject:
		if len(value.fields) > 0 {
			if shared := s.types[s.key(value)]; shared.declared() {
				return s.reference(shared)
			}
		}
	}
	return s.rewriteWithin(value)
}

func (s *sharing) rewriteWithin(value typ) typ {
	switch value.kind {
	case kindArray:
		item := s.rewrite(*value.item)
		value.item = &item
	case kindTuple:
		prefix := make([]typ, len(value.prefix))
		for i, item := range value.prefix {
			prefix[i] = s.rewrite(item)
		}
		value.prefix = prefix
	case kindObject:
		fields := make(map[string]field, len(value.fields))
		for _, name := range sortedKeys(value.fields) {
			member := value.fields[name]
			fields[name] = field{value: s.rewrite(member.value), required: member.required}
		}
		value.fields, value.definition = fields, ""
	case kindUnion, kindIntersection:
		items := make([]typ, len(value.items))
		for i, item := range value.items {
			items[i] = s.rewrite(item)
		}
		value.items = items
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

// spell writes value. A recursive type is spelled out where it is first reached and numbered after
// that, which spells equal types alike since everything is visited in order.
func spell(out *strings.Builder, value typ, seen map[*declaration]int) {
	fmt.Fprintf(out, "(%d", value.kind)
	defer out.WriteByte(')')
	switch value.kind {
	case kindDeclared:
		if number, ok := seen[value.ref]; ok {
			fmt.Fprintf(out, "#%d", number)
			return
		}
		seen[value.ref] = len(seen)
		spell(out, value.ref.value, seen)
	case kindLiteral:
		out.Write(encode(value.literal))
	case kindArray:
		spell(out, *value.item, seen)
	case kindTuple:
		fmt.Fprintf(out, "%d", value.min)
		for _, item := range value.prefix {
			spell(out, item, seen)
		}
	case kindObject:
		for _, name := range sortedKeys(value.fields) {
			member := value.fields[name]
			fmt.Fprintf(out, "%s%t", quoted(name), member.required)
			spell(out, member.value, seen)
		}
	case kindUnion, kindIntersection:
		for _, item := range value.items {
			spell(out, item, seen)
		}
	}
	if value.kind == kindTuple || value.kind == kindObject {
		fmt.Fprintf(out, "%d%t", value.extra.kind, value.extra.recursive)
		if value.extra.value != nil {
			spell(out, *value.extra.value, seen)
		}
	}
}

// compileCatalogs validates the binding names and compiles every contract's schemas.
func compileCatalogs(ctx context.Context, contracts map[string]contract) (map[string]catalog, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	catalogs := make(map[string]catalog, len(contracts))
	for _, name := range sortedKeys(contracts) {
		if name == "" {
			return nil, errors.New("binding names must be nonempty")
		}
		compiled, err := parseCatalog(ctx, contracts[name])
		if err != nil {
			return nil, err
		}
		catalogs[name] = compiled
	}
	return catalogs, nil
}
