package mcptype

import (
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"
)

var pythonKeywords = []string{
	"False", "None", "True", "and", "as", "assert", "async", "await", "break", "class",
	"continue", "def", "del", "elif", "else", "except", "finally", "for", "from", "global",
	"if", "import", "in", "is", "lambda", "nonlocal", "not", "or", "pass", "raise",
	"return", "try", "while", "with", "yield",
}

var pythonName = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// pythonIdentifier reports whether name is a public ASCII identifier that is not a keyword.
func pythonIdentifier(name string) bool {
	return pythonName.MatchString(name) && !strings.HasPrefix(name, "_") && !slices.Contains(pythonKeywords, name)
}

func pythonBlock(header string, lines []string, indent string) string {
	body := indent + "pass"
	if len(lines) > 0 {
		body = strings.Join(lines, "\n")
	}
	return header + "\n" + body
}

func pythonOverloaded(lines []string) []string {
	if len(lines) > 1 {
		for i, line := range lines {
			lines[i] = "    @_typing.overload\n" + line
		}
	}
	return lines
}

type pythonEmitter struct {
	shapes  []string
	names   map[string]bool
	emitted map[*declaration]bool // Shared types already written.
	// Python evaluates TypedDict fields and alias values on import, so while a shape's recursive
	// types are declared their names may not exist yet. Every other type is rendered after them, and
	// a shared type is written when first rendered, before whatever uses it.
	declaring bool
	merging   bool // Rendering an intersection whose recursive members are inlined.
}

// pythonTypedDict reports whether an object renders as a TypedDict rather than a dict.
func pythonTypedDict(value typ) bool {
	return value.kind == kindObject && value.extra.kind == extraClosed
}

// pythonDeclares reports whether a type is declared, which a shared dict is not.
func pythonDeclares(ref *declaration) bool { return ref.cyclic || pythonTypedDict(ref.value) }

// declare names a shape's declarations and writes its recursive types.
func (e *pythonEmitter) declare(s shape, tool, binding string) {
	e.declaring = true
	defer func() { e.declaring = false }()
	for _, ref := range s.name(e.names, tool, binding, pythonDeclares) {
		if ref.cyclic {
			e.emit(ref)
		}
	}
}

// emit writes a declaration.
func (e *pythonEmitter) emit(ref *declaration) {
	e.emitted[ref] = true
	if pythonTypedDict(ref.value) {
		delete(e.names, ref.name) // Rendering the TypedDict claims its name back.
		e.render(ref.value, ref.name)
	} else {
		rendered := e.render(ref.value, ref.name)
		e.shapes = append(e.shapes, fmt.Sprintf("%s: _typing.TypeAlias = %s", ref.name, rendered))
	}
}

func (e *pythonEmitter) render(value typ, name string) string {
	switch value.kind {
	case kindDeclared:
		if !pythonDeclares(value.ref) {
			return e.render(value.ref.value, name)
		}
		if !value.ref.cyclic && !e.emitted[value.ref] {
			e.emit(value.ref)
		}
		if e.declaring && value.ref.cyclic {
			return quoted(value.ref.name)
		}
		return value.ref.name
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
		return fmt.Sprintf("_builtins.list[%s]", e.render(*value.item, name+"Item"))
	case kindTuple:
		return "_builtins.list[_builtins.object]"
	case kindUnion:
		if len(value.items) == 0 {
			return "_typing.Never"
		}
		rendered := make([]string, len(value.items))
		for i, item := range value.items {
			rendered[i] = e.render(item, name)
		}
		// A quoted name does not support | at runtime.
		if e.declaring && slices.ContainsFunc(value.items, func(item typ) bool { return item.kind == kindDeclared && item.ref.cyclic }) {
			return "_typing.Union[" + strings.Join(rendered, ", ") + "]"
		}
		return strings.Join(rendered, " | ")
	case kindIntersection:
		// Only the outermost intersection inlines its recursive members, so that each renders once.
		// Shared members are inlined like any object.
		inline := !e.merging
		if inline {
			e.merging = true
			defer func() { e.merging = false }()
		}
		combined := map[string]field{}
		for _, item := range value.items {
			if item.kind == kindDeclared && (inline || !item.ref.cyclic) {
				item = item.ref.value
			}
			if item.kind != kindObject {
				return "_builtins.object"
			}
			for _, key := range sortedKeys(item.fields) {
				member := item.fields[key]
				if previous, ok := combined[key]; ok && !reflect.DeepEqual(previous, member) {
					return "_builtins.object"
				}
				combined[key] = member
			}
		}
		return e.render(objectType(combined, extra{kind: extraImplicit}), name)
	case kindObject:
		if !pythonTypedDict(value) {
			rendered := "_builtins.object"
			if len(value.fields) == 0 && value.extra.kind == extraOpen {
				rendered = e.render(*value.extra.value, name+"Value")
			}
			return fmt.Sprintf("_builtins.dict[_builtins.str, %s]", rendered)
		}
		fields := make([]string, 0, len(value.fields))
		for _, key := range sortedKeys(value.fields) {
			member := value.fields[key]
			rendered := e.render(member.value, name+typeIdentifier(key))
			if !member.required {
				rendered = fmt.Sprintf("_typing.NotRequired[%s]", rendered)
			}
			fields = append(fields, fmt.Sprintf("%s: %s", quoted(key), rendered))
		}
		name = unique(e.names, name)
		e.shapes = append(e.shapes, fmt.Sprintf("%s = _typing.TypedDict(%q, {%s})", name, name, strings.Join(fields, ", ")))
		return name
	}
	return "_builtins.object"
}

// input renders one input type and its keyword signatures. Unpack requires a
// TypedDict, so unions become overloads and open objects use their value type.
func (e *pythonEmitter) input(value typ, name string) (string, []string) {
	for value.kind == kindDeclared && !pythonTypedDict(value.ref.value) {
		value = value.ref.value
	}
	if value.kind == kindUnion && len(value.items) > 0 {
		var inputs, arguments []string
		for _, item := range value.items {
			input, variants := e.input(item, name)
			inputs = append(inputs, input)
			arguments = append(arguments, variants...)
		}
		return strings.Join(inputs, " | "), arguments
	}
	input, argument := e.render(value, name), "_builtins.object"
	if e.names[input] {
		argument = "_typing.Unpack[" + input + "]"
	} else if item, ok := strings.CutPrefix(input, "_builtins.dict[_builtins.str, "); ok {
		argument = strings.TrimSuffix(item, "]")
	}
	return input, []string{argument}
}

// generatePython writes the typing-only module body: shapes, export classes, protocols and _Clients.
func generatePython(contracts map[string]catalog) string {
	emitter := &pythonEmitter{names: map[string]bool{"Inputs": true, "Outputs": true, "InputTypes": true, "OutputTypes": true}, emitted: map[*declaration]bool{}}
	var clients []string
	exportNames := []string{"Inputs", "Outputs", "InputTypes", "OutputTypes"}
	exports := make([][]string, 4)
	var fields, indices []string
	for _, binding := range sortedKeys(contracts) {
		appName := unique(emitter.names, typeIdentifier(binding)+"App")
		toolsName := unique(emitter.names, typeIdentifier(binding)+"Tools")
		var methods, calls []string
		appExports := make([][]string, 4)
		tools := contracts[binding]
		for _, name := range sortedKeys(tools) {
			tool := tools[name]
			shapes := []shape{tool.input, tool.output}
			prefix := typeIdentifier(binding) + typeIdentifier(name)
			emitter.declare(shapes[0], prefix+"Input", typeIdentifier(binding)+"Input")
			emitter.declare(shapes[1], prefix+"Output", typeIdentifier(binding)+"Output")
			input, arguments := emitter.input(shapes[0].value, prefix+"Input")
			output := emitter.render(shapes[1].value, prefix+"Output")
			returned := output
			if !tool.structured {
				returned = "_CompleteResult[_builtins.object]"
			}
			var toolMethods []string
			for _, argument := range arguments {
				calls = append(calls, fmt.Sprintf("    async def call_tool(self, name: _typing.Literal[%s], options: _CallOptions | None = ..., /, **arguments: %s) -> _ToolResult[%s]: ...", quoted(name), argument, output))
				toolMethods = append(toolMethods, fmt.Sprintf("    async def %s(self, options: _CallOptions | None = ..., /, **arguments: %s) -> %s: ...", name, argument, returned))
			}
			if !pythonIdentifier(name) {
				continue
			}
			methods = append(methods, pythonOverloaded(toolMethods)...)
			for i, rendered := range []string{input, output} {
				if i == 1 && !tool.structured {
					continue
				}
				appExports[i] = append(appExports[i], fmt.Sprintf("        %s: _typing.TypeAlias = %s", name, rendered))
				var types []string
				for _, definition := range sortedKeys(shapes[i].named) {
					if pythonIdentifier(definition) {
						types = append(types, fmt.Sprintf("            %s: _typing.TypeAlias = %s", definition, emitter.render(shapes[i].named[definition], prefix+exportNames[i]+typeIdentifier(definition))))
					}
				}
				appExports[i+2] = append(appExports[i+2], pythonBlock(fmt.Sprintf("        class %s:", name), types, "            "))
			}
		}
		var app []string
		clients = append(clients, pythonBlock(fmt.Sprintf("class %s(_typing.Protocol):", toolsName), methods, "    "))
		app = append(app, "    tools: "+toolsName)
		app = append(app, pythonOverloaded(calls)...)
		clients = append(clients, pythonBlock(fmt.Sprintf("class %s(_typing.Protocol):", appName), app, "    "))
		if pythonIdentifier(binding) {
			fields = append(fields, fmt.Sprintf("    %s: %s", binding, appName))
			for i := range exports {
				exports[i] = append(exports[i], pythonBlock(fmt.Sprintf("    class %s:", binding), appExports[i], "        "))
			}
		}
		indices = append(indices, fmt.Sprintf("    def __getitem__(self, name: _typing.Literal[%s]) -> %s: ...", quoted(binding), appName))
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
