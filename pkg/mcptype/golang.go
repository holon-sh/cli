package mcptype

import (
	"fmt"
	"strings"
)

// goBinding is one contract's generated Go client: its variable name and method per tool.
type goBinding struct {
	name  string
	tools map[string]goTool
}

type goTool struct {
	name, input, output string
}

// goIdentifier turns any name into an exported Go identifier.
func goIdentifier(value string) string {
	var result strings.Builder
	upper := true
	for i := 0; i < len(value); i++ {
		c := value[i]
		if !isASCIIAlphanumeric(c) {
			upper = true
			continue
		}
		if result.Len() == 0 && isASCIIDigit(c) {
			result.WriteByte('X')
		}
		if upper && c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
		}
		result.WriteByte(c)
		upper = false
	}
	if result.Len() == 0 {
		return "Value"
	}
	return result.String()
}

func unique(names map[string]bool, preferred string) string {
	name := preferred
	for suffix := 2; names[name]; suffix++ {
		name = fmt.Sprintf("%s%d", preferred, suffix)
	}
	names[name] = true
	return name
}

// goFieldName reports whether value can be a struct tag's JSON name.
func goFieldName(value string) bool {
	if value == "" || value == "-" {
		return false
	}
	for i := 0; i < len(value); i++ {
		if c := value[i]; !isASCIIAlphanumeric(c) && !strings.ContainsRune(" !#$%&()*+-./:;<=>?@[]^_{|}~", rune(c)) {
			return false
		}
	}
	return true
}

func renderGo(value typ) string {
	switch value.kind {
	case kindString:
		return "string"
	case kindInteger, kindNumber:
		return "json.Number"
	case kindBoolean:
		return "bool"
	case kindLiteral:
		switch value.literal.(type) {
		case string:
			return "string"
		case bool:
			return "bool"
		case Number:
			return "json.Number"
		}
	case kindArray:
		return "[]" + renderGo(*value.item)
	case kindObject:
		if len(value.fields) == 0 {
			item := "json.RawMessage"
			if value.extra.kind == extraOpen {
				item = renderGo(*value.extra.value)
			}
			return "map[string]" + item
		}
		if value.extra.kind != extraClosed {
			return "map[string]json.RawMessage"
		}
		for name := range value.fields {
			if !goFieldName(name) {
				return "map[string]json.RawMessage"
			}
		}
		names := map[string]bool{}
		var out strings.Builder
		out.WriteString("struct {\n")
		for _, name := range sortedKeys(value.fields) {
			member := value.fields[name]
			rendered := renderGo(member.value)
			tagName := name
			if !member.required {
				if rendered != "json.RawMessage" {
					rendered = "*" + rendered
				}
				tagName += ",omitempty"
			}
			fmt.Fprintf(&out, "\t%s %s %s\n", unique(names, goIdentifier(name)), rendered, quoted("json:"+quoted(tagName)))
		}
		out.WriteString("}")
		return out.String()
	}
	return "json.RawMessage"
}

// generateGo writes the type declarations and names each binding's client and methods.
func generateGo(contracts map[string]catalog) (string, map[string]goBinding) {
	names := map[string]bool{}
	var declarations strings.Builder
	bindings := make(map[string]goBinding, len(contracts))
	for _, binding := range sortedKeys(contracts) {
		client := unique(names, goIdentifier(binding))
		methods := map[string]bool{"CallTool": true}
		tools := map[string]goTool{}
		catalog := contracts[binding].tools
		for _, name := range sortedKeys(catalog) {
			tool := catalog[name]
			method := unique(methods, goIdentifier(name))
			var symbols []string
			for _, part := range []struct {
				suffix string
				shape  shape
			}{{"Input", tool.input}, {"Output", tool.output}} {
				symbol := unique(names, client+method+part.suffix)
				fmt.Fprintf(&declarations, "type %s = %s\n\n", symbol, renderGo(part.shape.value))
				for _, definition := range sortedKeys(part.shape.named) {
					named := unique(names, symbol+goIdentifier(definition))
					fmt.Fprintf(&declarations, "type %s = %s\n\n", named, renderGo(part.shape.named[definition]))
				}
				symbols = append(symbols, symbol)
			}
			tools[name] = goTool{name: method, input: symbols[0], output: symbols[1]}
		}
		bindings[binding] = goBinding{name: client, tools: tools}
	}
	text := declarations.String()
	if strings.Contains(text, "json.") {
		text = "import \"encoding/json\"\n\n" + text
	}
	return text, bindings
}
