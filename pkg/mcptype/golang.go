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

// goStruct reports whether an object decodes as a struct: one that names fields with valid tags and
// allows no others.
func goStruct(value typ, input bool) bool {
	if value.kind != kindObject || len(value.fields) == 0 || value.extra.kind == extraOpen || value.extra.kind == extraImplicit && !input {
		return false
	}
	for name := range value.fields {
		if name == "-" || !goTagName.MatchString(name) {
			return false
		}
	}
	return true
}

// goDeclares reports whether a type is declared. A shared map is not, since maps of one type are
// interchangeable anyway.
func goDeclares(ref *declaration, input bool) bool {
	return ref.cyclic || goStruct(ref.value, input)
}

func renderGo(value typ, input bool) string {
	switch value.kind {
	case kindDeclared:
		if !goDeclares(value.ref, input) {
			return renderGo(value.ref.value, input)
		}
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
		if !goStruct(value, input) {
			return "map[string]json.RawMessage"
		}
		names := map[string]bool{}
		var out strings.Builder
		out.WriteString("struct {\n")
		for _, name := range sortedKeys(value.fields) {
			member := value.fields[name]
			rendered := renderGo(member.value, input)
			tagName := name
			// A struct cannot contain a recursive type by value, since that type may contain the struct.
			if (!member.required || member.value.kind == kindDeclared && member.value.ref.cyclic) && rendered != "json.RawMessage" {
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
				declares := func(ref *declaration) bool { return goDeclares(ref, input) }
				root := part.shape.value
				// A type that this tool uses first as its whole input or output is declared as that.
				if root.kind == kindDeclared && root.ref.name == "" && declares(root.ref) && root.ref.preferred(symbol, client+part.suffix) == symbol {
					root.ref.name = symbol
				}
				for _, ref := range part.shape.name(names, symbol, client+part.suffix, declares) {
					// Only a defined type can contain itself, but one defined from json.RawMessage would
					// lose its methods.
					format := "type %s = %s\n\n"
					if ref.cyclic && goRecursive(ref.value) {
						format = "type %s %s\n\n"
					}
					fmt.Fprintf(&declarations, format, ref.name, renderGo(ref.value, input))
				}
				if root.kind != kindDeclared || root.ref.name != symbol {
					fmt.Fprintf(&declarations, "type %s = %s\n\n", symbol, renderGo(root, input))
				}
				for _, definition := range sortedKeys(part.shape.named) {
					value, preferred := part.shape.named[definition], symbol+typeIdentifier(definition)
					if value.kind == kindDeclared && value.ref.name == preferred {
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
