package mcptype

import (
	"cmp"
	"context"
	"slices"
)

// Parameter is one property of a tool's input schema, read as the generated clients read it.
type Parameter struct {
	Name     string
	Required bool
	// Type is the parameter's TypeScript type. A recursive type is named by its definition.
	Type string
	// Text reports that every value the parameter takes is a string or null.
	Text bool
}

// Parameters returns the properties of a tool's input schema, by name. A schema that is not an
// object has none.
func Parameters(ctx context.Context, inputSchema []byte) ([]Parameter, error) {
	schema, err := decodeJSON(inputSchema)
	if err != nil {
		return nil, err
	}
	shape, err := parseShape(ctx, schema)
	if err != nil {
		return nil, err
	}
	for _, ref := range shape.declared {
		ref.name = cmp.Or(ref.definition, "object")
	}
	root := shape.value
	if root.kind == kindDeclared {
		root = root.ref.value
	}
	parameters := make([]Parameter, 0, len(root.fields))
	for _, name := range sortedKeys(root.fields) {
		field := root.fields[name]
		parameters = append(parameters, Parameter{Name: name, Required: field.required, Type: renderTypeScript(field.value), Text: text(field.value, map[*declaration]bool{})})
	}
	return parameters, nil
}

// text reports whether every value value admits is a string or null, and some are strings.
func text(value typ, seen map[*declaration]bool) bool {
	switch value.kind {
	case kindString:
		return true
	case kindLiteral:
		_, ok := value.literal.(string)
		return ok
	case kindUnion:
		items := slices.DeleteFunc(slices.Clone(value.items), func(item typ) bool { return item.kind == kindNull })
		return len(items) > 0 && !slices.ContainsFunc(items, func(item typ) bool { return !text(item, seen) })
	case kindIntersection:
		return slices.ContainsFunc(value.items, func(item typ) bool { return text(item, seen) })
	case kindDeclared:
		if seen[value.ref] {
			return false
		}
		seen[value.ref] = true
		return text(value.ref.value, seen)
	}
	return false
}
