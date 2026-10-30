package mcptype

import (
	"fmt"
	"strings"
)

func renderTypeScript(value typ) string {
	switch value.kind {
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
			items = append(items, fmt.Sprintf("...Array<%s>", renderTypeScript(*value.extra.value)))
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

func joinTypeScript(items []typ, separator string) string {
	rendered := make([]string, len(items))
	for i, item := range items {
		rendered[i] = renderTypeScript(item)
	}
	return strings.Join(rendered, separator)
}

// generateTypeScript writes the Inputs, Outputs, InputTypes and OutputTypes interfaces.
// A tool without an outputSchema has no Outputs or OutputTypes entry; ToolClient then
// types its convenience call as the complete result.
func generateTypeScript(contracts map[string]catalog) string {
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
	var out strings.Builder
	for i := range exports {
		out.WriteString(exports[i].String())
		out.WriteString("}\n")
	}
	return out.String()
}
