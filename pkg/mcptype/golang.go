package mcptype

import (
	"cmp"
	"fmt"
	"regexp"
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

var goWords = regexp.MustCompile(`[a-zA-Z0-9]+`)

// goTagName matches ASCII JSON struct-tag names; "-" alone means omission.
var goTagName = regexp.MustCompile(`^[a-zA-Z0-9` + regexp.QuoteMeta(" !#$%&()*+./:;<=>?@[]^_{|}~") + `-]+$`)

// typeIdentifier turns any name into a capitalized ASCII identifier.
func typeIdentifier(value string) string {
	words := goWords.FindAllString(value, -1)
	for i, word := range words {
		words[i] = strings.ToUpper(word[:1]) + word[1:]
	}
	name := strings.Join(words, "")
	if name != "" && name[0] >= '0' && name[0] <= '9' {
		name = "X" + name
	}
	return cmp.Or(name, "Value")
}

func unique(names map[string]bool, preferred string) string {
	name := preferred
	for suffix := 2; names[name]; suffix++ {
		name = fmt.Sprintf("%s%d", preferred, suffix)
	}
	names[name] = true
	return name
}

// goRecursive reports whether a recursive type keeps its recursion in Go, which arrays and objects
// do. The others are unions, intersections and tuples, which decode as raw JSON as usual.
func goRecursive(value typ) bool {
	return value.kind == kindArray || value.kind == kindObject
}

func renderGo(value typ, input bool) string {
	switch value.kind {
	case kindRecursive:
		if !goRecursive(value.ref.value) {
			return "json.RawMessage"
		}
		return value.ref.name
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
		return "[]" + renderGo(*value.item, input)
	case kindObject:
		if len(value.fields) == 0 {
			item := "json.RawMessage"
			if value.extra.kind == extraOpen {
				item = renderGo(*value.extra.value, input)
			}
			return "map[string]" + item
		}
		if value.extra.kind == extraOpen || value.extra.kind == extraImplicit && !input {
			return "map[string]json.RawMessage"
		}
		for name := range value.fields {
			if name == "-" || !goTagName.MatchString(name) {
				return "map[string]json.RawMessage"
			}
		}
		names := map[string]bool{}
		var out strings.Builder
		out.WriteString("struct {\n")
		for _, name := range sortedKeys(value.fields) {
			member := value.fields[name]
			rendered := renderGo(member.value, input)
			tagName := name
			// A struct cannot contain a recursive type by value, since that type may contain the struct.
			if (!member.required || member.value.kind == kindRecursive) && rendered != "json.RawMessage" {
				rendered = "*" + rendered
			}
			if !member.required {
				tagName += ",omitempty"
			}
			fmt.Fprintf(&out, "\t%s %s %s\n", unique(names, typeIdentifier(name)), rendered, quoted("json:"+quoted(tagName)))
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
		client := unique(names, typeIdentifier(binding))
		methods := map[string]bool{"CallTool": true}
		tools := map[string]goTool{}
		catalog := contracts[binding]
		for _, name := range sortedKeys(catalog) {
			tool := catalog[name]
			method := unique(methods, typeIdentifier(name))
			var symbols []string
			for index, part := range []struct {
				suffix string
				shape  shape
			}{{"Input", tool.input}, {"Output", tool.output}} {
				input := index == 0
				symbol := unique(names, client+method+part.suffix)
				for _, ref := range part.shape.nameRecursive(names, symbol) {
					// Only a defined type can contain itself, but one defined from json.RawMessage would
					// lose its methods.
					format := "type %s = %s\n\n"
					if goRecursive(ref.value) {
						format = "type %s %s\n\n"
					}
					fmt.Fprintf(&declarations, format, ref.name, renderGo(ref.value, input))
				}
				fmt.Fprintf(&declarations, "type %s = %s\n\n", symbol, renderGo(part.shape.value, input))
				for _, definition := range sortedKeys(part.shape.named) {
					value, preferred := part.shape.named[definition], symbol+typeIdentifier(definition)
					if value.kind == kindRecursive && value.ref.name == preferred {
						continue
					}
					fmt.Fprintf(&declarations, "type %s = %s\n\n", unique(names, preferred), renderGo(value, input))
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
