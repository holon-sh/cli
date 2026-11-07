package mcptype

import (
	"fmt"
	"strings"
)

// renderTypeScript writes a type. A free-form object is an index signature of unknown, and so is an
// open object that names fields, which would otherwise have to fit the signature's type.
func renderTypeScript(value typ) string {
	switch value.kind {
	case kindDeclared:
		return value.ref.name
	case kindNever:
		return "never"
	case kindString:
		return "string"
	case kindInteger, kindNumber:
		return "number"
	case kindBoolean:
		return "boolean"
	case kindNull:
		return "null"
	case kindLiteral:
		switch value.literal.(type) {
		case []Value, Object:
			return "unknown"
		}
		return string(encode(value.literal))
	case kindArray:
		return fmt.Sprintf("Array<%s>", renderTypeScript(*value.item))
	case kindUnion:
		if len(value.items) == 0 {
			return "never"
		}
		return "(" + joinTypeScript(value.items, " | ") + ")"
	case kindIntersection:
		if len(value.items) == 0 {
			return "unknown"
		}
		return "(" + joinTypeScript(value.items, " & ") + ")"
	case kindTuple:
		items := make([]string, 0, len(value.prefix)+1)
		for i, item := range value.prefix {
			optional := ""
			if i >= value.min {
				optional = "?"
			}
			items = append(items, renderTypeScript(item)+optional)
		}
		switch value.extra.kind {
		case extraImplicit:
			items = append(items, "...unknown[]")
		case extraOpen:
			rest := renderTypeScript(*value.extra.value)
			if value.extra.recursive {
				// tsc rejects `type T = [A, ...Array<T>]` but accepts this spelling.
				items = append(items, "..."+rest+"[]")
			} else {
				items = append(items, fmt.Sprintf("...Array<%s>", rest))
			}
		}
		return "[" + strings.Join(items, ", ") + "]"
	case kindObject:
		entries := make([]string, 0, len(value.fields)+1)
		for _, name := range sortedKeys(value.fields) {
			field := value.fields[name]
			optional := ""
			if !field.required {
				optional = "?"
			}
			entries = append(entries, fmt.Sprintf("%s%s: %s;", quoted(name), optional, renderTypeScript(field.value)))
		}
		switch value.extra.kind {
		case extraImplicit:
			entries = append(entries, "[key: string]: unknown;")
		case extraOpen:
			rendered := "unknown"
			if len(value.fields) == 0 {
				rendered = renderTypeScript(*value.extra.value)
			}
			entries = append(entries, fmt.Sprintf("[key: string]: %s;", rendered))
		}
		return "{ " + strings.Join(entries, " ") + " }"
	}
	return "unknown"
}

func declaresAll(*declaration) bool { return true }

func joinTypeScript(items []typ, separator string) string {
	rendered := make([]string, len(items))
	for i, item := range items {
		rendered[i] = renderTypeScript(item)
	}
	return strings.Join(rendered, separator)
}

// generateTypeScript writes the declared types, then the Inputs, Outputs, InputTypes and OutputTypes
// interfaces. Declared types take identifiers that names does not hold yet, and names keeps them.
func generateTypeScript(contracts map[string]catalog, names map[string]bool) string {
	var out strings.Builder
	exports := make([]strings.Builder, 4)
	for i, name := range []string{"Inputs", "Outputs", "InputTypes", "OutputTypes"} {
		fmt.Fprintf(&exports[i], "export interface %s {\n", name)
	}
	for _, binding := range sortedKeys(contracts) {
		for i := range exports {
			fmt.Fprintf(&exports[i], "  %s: {\n", quoted(binding))
		}
		tools := contracts[binding]
		for _, name := range sortedKeys(tools) {
			tool := tools[name]
			for index, shape := range []shape{tool.input, tool.output} {
				if index == 1 && !tool.structured {
					continue
				}
				direction := []string{"Input", "Output"}[index]
				for _, ref := range shape.name(names, typeIdentifier(binding)+typeIdentifier(name)+direction, typeIdentifier(binding)+direction, declaresAll) {
					fmt.Fprintf(&out, "export type %s = %s;\n", ref.name, renderTypeScript(ref.value))
				}
				fmt.Fprintf(&exports[index], "    %s: %s;\n", quoted(name), renderTypeScript(shape.value))
				named := make([]string, 0, len(shape.named))
				for _, definition := range sortedKeys(shape.named) {
					named = append(named, fmt.Sprintf("%s: %s;", quoted(definition), renderTypeScript(shape.named[definition])))
				}
				fmt.Fprintf(&exports[index+2], "    %s: { %s };\n", quoted(name), strings.Join(named, " "))
			}
		}
		for i := range exports {
			exports[i].WriteString("  };\n")
		}
	}
	for i := range exports {
		out.WriteString(exports[i].String())
		out.WriteString("}\n")
	}
	return out.String()
}
