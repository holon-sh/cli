package mcptype

import (
	"fmt"
	"strconv"
	"strings"
)

// The generated Go package holon: declarations and one client variable per dependency.
// clients.go is gofmt-clean by construction; the declarations in types.go are not reformatted.

func generateGoPackage(request generateRequest, catalogs map[string]catalog) map[string]string {
	names := map[string]bool{}
	var declarations, source strings.Builder
	hasTools := false
	for _, binding := range sortedKeys(catalogs) {
		client := unique(names, typeIdentifier(binding))
		taken := map[string]bool{"CallTool": true}
		tools := catalogs[binding]
		nameDeclarations(names, binding, tools, goDeclares)
		var methods strings.Builder
		fmt.Fprintf(&source, "\nvar %s = _%s{&holon.Dependency{Name: %s, Tools: map[string]holon.ToolSchema{", client, client, strconv.Quote(binding))
		if len(tools) > 0 {
			source.WriteByte('\n')
			hasTools = true
		}
		for _, name := range sortedKeys(tools) {
			tool, schema := tools[name], request.contracts[binding].Tools[name]
			method := unique(taken, typeIdentifier(name))
			symbols := []string{unique(names, client+method+"Input"), unique(names, client+method+"Output")}
			for i, shape := range []shape{tool.input, tool.output} {
				for _, ref := range shape.declarations(goDeclares) {
					// Only a defined type can contain itself, but one defined from json.RawMessage would
					// lose its methods.
					format := "type %s = %s\n\n"
					if ref.cyclic && goRecursive(ref.value) {
						format = "type %s %s\n\n"
					}
					fmt.Fprintf(&declarations, format, ref.name, renderGo(ref.value))
				}
				fmt.Fprintf(&declarations, "type %s = %s\n\n", symbols[i], renderGo(shape.value))
			}
			output := `""`
			if schema.Output != nil {
				output = strconv.Quote(string(encode(schema.Output)))
			}
			fmt.Fprintf(&source, "\t%s: {\n\t\tInput:  %s,\n\t\tOutput: %s,\n\t},\n", strconv.Quote(name), strconv.Quote(string(encode(schema.Input))), output)
			fmt.Fprintf(&methods, "\nfunc (client _%s) %s(ctx context.Context, input %s) (%s, error) {\n\treturn holon.Call[%s](ctx, client._dependency, %s, input)\n}\n", client, method, symbols[0], symbols[1], symbols[1], strconv.Quote(name))
		}
		fmt.Fprintf(&source, "}}}\n\ntype _%s struct{ *_dependency }\n%s", client, methods.String())
	}
	var imports []string
	if hasTools {
		imports = append(imports, "\t\"context\"\n")
	}
	text, types := source.String(), declarations.String()
	if len(catalogs) > 0 {
		imports = append(imports, fmt.Sprintf("\tholon %s\n", strconv.Quote(request.GoModule)))
		text = "\ntype _dependency = holon.Dependency\n" + text
	}
	header := ""
	if len(imports) > 0 {
		header = fmt.Sprintf("\nimport (\n%s)\n", strings.Join(imports, "\n"))
	}
	if strings.Contains(types, "json.") {
		types = "import \"encoding/json\"\n\n" + types
	}
	return map[string]string{
		"types.go":   "package holon\n\n" + types,
		"clients.go": "package holon\n" + header + text,
	}
}
