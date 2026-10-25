package mcptype

import (
	"fmt"
	"slices"
	"strings"
)

var pythonKeywords = []string{
	"False", "None", "True", "and", "as", "assert", "async", "await", "break", "class",
	"continue", "def", "del", "elif", "else", "except", "finally", "for", "from", "global",
	"if", "import", "in", "is", "lambda", "nonlocal", "not", "or", "pass", "raise",
	"return", "try", "while", "with", "yield",
}

func isASCIIAlphabetic(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
func isASCIIDigit(c byte) bool      { return c >= '0' && c <= '9' }
func isASCIIAlphanumeric(c byte) bool {
	return isASCIIAlphabetic(c) || isASCIIDigit(c)
}

// pythonIdentifier reports whether name is a plain ASCII identifier that is not a keyword.
func pythonIdentifier(name string) bool {
	if name == "" || !isASCIIAlphabetic(name[0]) {
		return false
	}
	for i := 0; i < len(name); i++ {
		if c := name[i]; !isASCIIAlphanumeric(c) && c != '_' {
			return false
		}
	}
	return !slices.Contains(pythonKeywords, name)
}

func pythonBlock(header string, lines []string, indent string) string {
	body := indent + "pass"
	if len(lines) > 0 {
		body = strings.Join(lines, "\n")
	}
	return header + "\n" + body
}

func pythonOverloaded(lines []string) []string {
	if len(lines) <= 1 {
		return lines
	}
	result := make([]string, len(lines))
	for i, line := range lines {
		result[i] = "    @_typing.overload\n" + line
	}
	return result
}

type pythonEmitter struct {
	shapes []string
}

func (e *pythonEmitter) render(value typ) string {
	switch value.kind {
	case kindNever:
		return "_typing.Never"
	case kindString:
		return "_builtins.str"
	case kindInteger:
		return "_builtins.int"
	case kindNumber:
		return "_builtins.float"
	case kindBoolean:
		return "_builtins.bool"
	case kindNull:
		return "None"
	case kindLiteral:
		switch literal := value.literal.(type) {
		case string:
			return fmt.Sprintf("_typing.Literal[%s]", quoted(literal))
		case bool:
			if literal {
				return "_typing.Literal[True]"
			}
			return "_typing.Literal[False]"
		case nil:
			return "None"
		case Number:
			if literal.isInteger() {
				return fmt.Sprintf("_typing.Literal[%s]", literal)
			}
			return "_builtins.float"
		}
		return "_builtins.object"
	case kindArray:
		return fmt.Sprintf("_builtins.list[%s]", e.render(*value.item))
	case kindTuple:
		return "_builtins.list[_builtins.object]"
	case kindUnion:
		if len(value.items) == 0 {
			return "_typing.Never"
		}
		rendered := make([]string, len(value.items))
		for i, item := range value.items {
			rendered[i] = e.render(item)
		}
		return strings.Join(rendered, " | ")
	case kindIntersection:
		combined := map[string]field{}
		for _, item := range value.items {
			if item.kind != kindObject {
				return "_builtins.object"
			}
			for _, key := range sortedKeys(item.fields) {
				member := item.fields[key]
				if previous, ok := combined[key]; ok && (previous.required != member.required || !typeEqual(previous.value, member.value)) {
					return "_builtins.object"
				}
				combined[key] = member
			}
		}
		return e.render(typ{kind: kindObject, fields: combined, extra: extra{kind: extraImplicit}})
	case kindObject:
		if len(value.fields) == 0 || value.extra.kind == extraOpen {
			rendered := "_builtins.object"
			if len(value.fields) == 0 && value.extra.kind == extraOpen {
				rendered = e.render(*value.extra.value)
			}
			return fmt.Sprintf("_builtins.dict[_builtins.str, %s]", rendered)
		}
		fields := make([]string, 0, len(value.fields))
		for _, key := range sortedKeys(value.fields) {
			member := value.fields[key]
			rendered := e.render(member.value)
			if !member.required {
				rendered = fmt.Sprintf("_typing.NotRequired[%s]", rendered)
			}
			fields = append(fields, fmt.Sprintf("%s: %s", quoted(key), rendered))
		}
		name := fmt.Sprintf("_Shape%d", len(e.shapes))
		e.shapes = append(e.shapes, fmt.Sprintf("%s = _typing.TypedDict(%q, {%s})", name, name, strings.Join(fields, ", ")))
		return name
	}
	return "_builtins.object"
}

// generatePython writes the typing-only module body: shapes, export classes, protocols and _Clients.
func generatePython(contracts map[string]catalog) string {
	emitter := &pythonEmitter{}
	var clients []string
	exportNames := []string{"Inputs", "Outputs", "InputTypes", "OutputTypes"}
	exports := make([][]string, 4)
	var fields, indices []string
	for index, binding := range sortedKeys(contracts) {
		var methods, calls []string
		appExports := make([][]string, 4)
		tools := contracts[binding].tools
		for _, name := range sortedKeys(tools) {
			tool := tools[name]
			shapes := []shape{tool.input, tool.output}
			input, output := emitter.render(shapes[0].value), emitter.render(shapes[1].value)
			calls = append(calls, fmt.Sprintf("    async def call_tool(self, name: _typing.Literal[%s], arguments: %s, *, timeout: _builtins.float | None = ..., input_responses: _InputResponses | None = ..., request_state: _builtins.str | None = ..., meta: _RequestParamsMeta | None = ...) -> _ToolResult[%s]: ...", quoted(name), input, output))
			if !pythonIdentifier(name) {
				continue
			}
			methods = append(methods, fmt.Sprintf("    async def %s(self, arguments: %s, *, timeout: _builtins.float | None = ...) -> %s: ...", name, input, output))
			for i, rendered := range []string{input, output} {
				appExports[i] = append(appExports[i], fmt.Sprintf("        %s: _typing.TypeAlias = %s", name, rendered))
				var types []string
				for _, definition := range sortedKeys(shapes[i].named) {
					if pythonIdentifier(definition) {
						types = append(types, fmt.Sprintf("            %s: _typing.TypeAlias = %s", definition, emitter.render(shapes[i].named[definition])))
					}
				}
				appExports[i+2] = append(appExports[i+2], pythonBlock(fmt.Sprintf("        class %s:", name), types, "            "))
			}
		}
		clients = append(clients, pythonBlock(fmt.Sprintf("class _Tools%d(_typing.Protocol):", index), methods, "    "))
		app := append([]string{fmt.Sprintf("    tools: _Tools%d", index)}, pythonOverloaded(calls)...)
		clients = append(clients, pythonBlock(fmt.Sprintf("class _App%d(_typing.Protocol):", index), app, "    "))
		if pythonIdentifier(binding) {
			fields = append(fields, fmt.Sprintf("    %s: _App%d", binding, index))
			for i := range exports {
				exports[i] = append(exports[i], pythonBlock(fmt.Sprintf("    class %s:", binding), appExports[i], "        "))
			}
		}
		indices = append(indices, fmt.Sprintf("    def __getitem__(self, name: _typing.Literal[%s]) -> _App%d: ...", quoted(binding), index))
	}
	sections := []string{"import builtins as _builtins\nimport typing as _typing"}
	sections = append(sections, emitter.shapes...)
	for i, name := range exportNames {
		sections = append(sections, pythonBlock(fmt.Sprintf("class %s:", name), exports[i], "    "))
	}
	sections = append(sections, clients...)
	fields = append(fields, pythonOverloaded(indices)...)
	sections = append(sections, pythonBlock("class _Clients(_typing.Protocol):", fields, "    "))
	return strings.Join(sections, "\n\n") + "\n"
}
