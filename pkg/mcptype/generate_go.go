package mcptype

import (
	"fmt"
	"strconv"
	"strings"
)

// The generated Go package holon: declarations and one client variable per dependency.
// clients.go is gofmt-clean by construction; the declarations in types.go are not reformatted.

func generateGoPackage(request generateRequest, catalogs map[string]catalog) map[string]string {
	declarations, bindings := generateGo(catalogs)
	var source strings.Builder
	hasTools := false
	for _, alias := range sortedKeys(request.contracts) {
		contract, binding := request.contracts[alias], bindings[alias]
		name := binding.name
		fmt.Fprintf(&source, "\nvar %s = _%s{&holon.Dependency{Name: %s, Tools: map[string]holon.ToolSchema{", name, name, strconv.Quote(alias))
		if len(contract.Tools) > 0 {
			source.WriteByte('\n')
			hasTools = true
		}
		for _, tool := range sortedKeys(contract.Tools) {
			schema := contract.Tools[tool]
			output := `""`
			if schema.Output != nil {
				output = strconv.Quote(string(encode(schema.Output)))
			}
			fmt.Fprintf(&source, "\t%s: {\n\t\tInput:  %s,\n\t\tOutput: %s,\n\t},\n", strconv.Quote(tool), strconv.Quote(string(encode(schema.Input))), output)
		}
		fmt.Fprintf(&source, "}}}\n\ntype _%s struct{ *_dependency }\n", name)
		for _, tool := range sortedKeys(binding.tools) {
			method := binding.tools[tool]
			fmt.Fprintf(&source, "\nfunc (client _%s) %s(ctx context.Context, input %s) (%s, error) {\n\treturn holon.Call[%s](ctx, client._dependency, %s, input)\n}\n", name, method.name, method.input, method.output, method.output, strconv.Quote(tool))
		}
	}
	var imports []string
	if hasTools {
		imports = append(imports, "\t\"context\"\n")
	}
	text := source.String()
	if len(request.contracts) > 0 {
		imports = append(imports, fmt.Sprintf("\tholon %s\n", strconv.Quote(request.GoModule)))
		text = "\ntype _dependency = holon.Dependency\n" + text
	}
	header := ""
	if len(imports) > 0 {
		header = fmt.Sprintf("\nimport (\n%s)\n", strings.Join(imports, "\n"))
	}
	return map[string]string{
		"types.go":   "package holon\n\n" + declarations,
		"clients.go": "package holon\n" + header + text,
	}
}
