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

var asciiWords = regexp.MustCompile(`[a-zA-Z0-9]+`)

// goTagName matches ASCII JSON struct-tag names; "-" alone means omission.
var goTagName = regexp.MustCompile(`^[a-zA-Z0-9` + regexp.QuoteMeta(" !#$%&()*+./:;<=>?@[]^_{|}~") + `-]+$`)

// typeIdentifier turns any name into a capitalized ASCII identifier.
func typeIdentifier(value string) string {
	words := asciiWords.FindAllString(value, -1)
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

// goStruct reports whether an object decodes as a struct: one that names fields with valid tags and
// declares no schema for other properties.
func goStruct(value typ) bool {
	if value.kind != kindObject || len(value.fields) == 0 || value.extra.kind == extraOpen {
		return false
	}
	for name := range value.fields {
		if name == "-" || !goTagName.MatchString(name) {
			return false
		}
	}
	return true
}

// goDeclares reports whether a type is declared. A shared map that nothing names is not, since maps
// of one type are interchangeable anyway.
func goDeclares(ref *declaration) bool {
	return ref.cyclic || ref.definition != "" || goStruct(ref.value)
}

// goRawJSON reports whether a type decodes as raw JSON, by whatever name.
func goRawJSON(value typ) bool {
	for value.kind == kindDeclared && !value.ref.cyclic {
		value = value.ref.value
	}
	return renderGo(value) == "json.RawMessage"
}

func renderGo(value typ) string {
	switch value.kind {
	case kindDeclared:
		if !goDeclares(value.ref) {
			return renderGo(value.ref.value)
		}
		if value.ref.cyclic && !goRecursive(value.ref.value) {
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
		return "[]" + renderGo(*value.item)
	case kindObject:
		if len(value.fields) == 0 {
			item := "json.RawMessage"
			if value.extra.kind == extraOpen {
				item = renderGo(*value.extra.value)
			}
			return "map[string]" + item
		}
		if !goStruct(value) {
			return "map[string]json.RawMessage"
		}
		names := map[string]bool{}
		var out strings.Builder
		out.WriteString("struct {\n")
		for _, name := range sortedKeys(value.fields) {
			member := value.fields[name]
			rendered := renderGo(member.value)
			tagName := name
			// A struct cannot contain a recursive type by value, since that type may contain the struct.
			if (!member.required || member.value.kind == kindDeclared && member.value.ref.cyclic) && !goRawJSON(member.value) {
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
		nameDeclarations(names, binding, catalog, goDeclares)
		for _, name := range sortedKeys(catalog) {
			tool := catalog[name]
			method := unique(methods, typeIdentifier(name))
			var symbols []string
			for _, part := range []struct {
				suffix string
				shape  shape
			}{{"Input", tool.input}, {"Output", tool.output}} {
				symbol := unique(names, client+method+part.suffix)
				root := part.shape.value
				for _, ref := range part.shape.declarations(goDeclares) {
					// Only a defined type can contain itself, but one defined from json.RawMessage would
					// lose its methods.
					format := "type %s = %s\n\n"
					if ref.cyclic && goRecursive(ref.value) {
						format = "type %s %s\n\n"
					}
					fmt.Fprintf(&declarations, format, ref.name, renderGo(ref.value))
				}
				fmt.Fprintf(&declarations, "type %s = %s\n\n", symbol, renderGo(root))
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
