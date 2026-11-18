package mcptype

import (
	"cmp"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

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
	value = goMember(value)
	return value.kind == kindArray || value.kind == kindObject
}

// goMember is a type without the unions and intersections of one member around it, which are that
// member.
func goMember(value typ) typ {
	for (value.kind == kindUnion || value.kind == kindIntersection) && len(value.items) == 1 {
		value = value.items[0]
	}
	return value
}

func goStruct(value typ) bool { return strings.HasPrefix(renderGo(value), "struct") }

// goRawJSON reports whether a type decodes as raw JSON, by whatever name.
func goRawJSON(value typ) bool {
	for value.kind == kindDeclared && !value.ref.cyclic {
		value = value.ref.value
	}
	return renderGo(value) == "json.RawMessage"
}

func renderGo(value typ) string {
	switch value = goMember(value); value.kind {
	case kindDeclared:
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
		case json.Number:
			return "json.Number"
		}
	case kindArray:
		return "[]" + renderGo(value.items[0])
	case kindObject:
		if len(value.fields) == 0 {
			item := "json.RawMessage"
			if value.extra.kind == extraOpen {
				item = renderGo(*value.extra.value)
			}
			return "map[string]" + item
		}
		if value.extra.kind == extraOpen {
			return "map[string]json.RawMessage"
		}
		names := map[string]bool{}
		var out strings.Builder
		out.WriteString("struct {\n")
		for _, name := range sortedKeys(value.fields) {
			if name == "-" || !goTagName.MatchString(name) {
				return "map[string]json.RawMessage"
			}
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
