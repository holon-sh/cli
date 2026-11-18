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
	return value.kind == kindArray || value.kind == kindObject
}

func goStruct(value typ) bool { return strings.HasPrefix(renderGo(value), "struct") }

// goNilable reports whether renderGo writes a type that holds null itself, by whatever name: raw
// JSON, or a pointer.
func goNilable(value typ) bool {
	switch value.kind {
	case kindDeclared:
		if value.ref.cyclic {
			return !goRecursive(value.ref.value)
		}
		return goNilable(value.ref.value)
	case kindString, kindInteger, kindNumber, kindBoolean, kindArray, kindObject:
		return false
	case kindLiteral:
		switch value.literal.(type) {
		case string, bool, json.Number:
			return false
		}
	case kindUnion, kindIntersection:
		if len(value.items) == 1 {
			return goNilable(value.items[0])
		}
	}
	return true
}

// goPointer renders a type that may also be null or absent: a pointer, unless it holds null itself.
func goPointer(value typ) string {
	if goNilable(value) {
		return renderGo(value)
	}
	return "*" + renderGo(value)
}

// goNullable reports the one type besides null that a union allows, if it allows no other. Go
// declares no other unions, which decode as raw JSON.
func goNullable(value typ) (typ, bool) {
	if value.kind != kindUnion || len(value.items) != 2 {
		return typ{}, false
	}
	if value.items[0].kind == kindNull {
		return value.items[1], true
	}
	return value.items[0], value.items[1].kind == kindNull
}

func renderGo(value typ) string {
	switch value.kind {
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
	case kindUnion, kindIntersection:
		if len(value.items) == 1 {
			return renderGo(value.items[0])
		}
		if other, ok := goNullable(value); ok {
			return goPointer(other)
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
			render := renderGo
			tagName := name
			// A struct cannot contain a recursive type by value, since that type may contain the struct.
			if !member.required || member.value.kind == kindDeclared && member.value.ref.cyclic {
				render = goPointer
			}
			rendered := render(member.value)
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
