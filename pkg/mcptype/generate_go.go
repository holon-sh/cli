package mcptype

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// The generated Go package holon: declarations and one client variable per dependency.
// clients.go is gofmt-clean by construction; the declarations in types.go are not reformatted.

func generateGoPackage(ctx context.Context, request generateRequest) (map[string]string, error) {
	if request.goModule == nil {
		return nil, errors.New("go_module is required for the go target")
	}
	catalogs, err := compileCatalogs(ctx, request.contracts)
	if err != nil {
		return nil, err
	}
	declarations, bindings := generateGo(catalogs)
	var source strings.Builder
	hasTools := false
	for _, alias := range sortedKeys(request.contracts) {
		contract, binding := request.contracts[alias], bindings[alias]
		name := binding.name
		fmt.Fprintf(&source, "\nvar %s = _%s{&holon.Dependency{Name: %s, Tools: map[string]holon.ToolSchema{", name, name, goQuote(alias))
		if len(contract.Tools) > 0 {
			source.WriteByte('\n')
			hasTools = true
		}
		for _, tool := range sortedKeys(contract.Tools) {
			schema := contract.Tools[tool]
			output := `""`
			if schema.Output != nil {
				output = goQuote(string(encode(schema.Output)))
			}
			fmt.Fprintf(&source, "\t%s: {\n\t\tInput:  %s,\n\t\tOutput: %s,\n\t},\n", goQuote(tool), goQuote(string(encode(schema.Input))), output)
		}
		fmt.Fprintf(&source, "}}}\n\ntype _%s struct{ *_dependency }\n", name)
		for _, tool := range sortedKeys(binding.tools) {
			method := binding.tools[tool]
			fmt.Fprintf(&source, "\nfunc (client _%s) %s(ctx context.Context, input %s) (%s, error) {\n\treturn holon.Call[%s](ctx, client._dependency, %s, input)\n}\n", name, method.name, method.input, method.output, method.output, goQuote(tool))
		}
	}
	var imports []string
	if hasTools {
		imports = append(imports, "\t\"context\"\n")
	}
	text := source.String()
	if len(request.contracts) > 0 {
		imports = append(imports, fmt.Sprintf("\tholon %s\n", goQuote(*request.goModule)))
		text = "\ntype _dependency = holon.Dependency\n" + text
	}
	header := ""
	if len(imports) > 0 {
		header = fmt.Sprintf("\nimport (\n%s)\n", strings.Join(imports, "\n"))
	}
	return map[string]string{
		"types.go":     "package holon\n\n" + declarations,
		"clients.go":   "package holon\n" + header + text,
		"package.json": string(encode(request.packageFields)),
	}, nil
}

// goQuote is a Go interpreted string literal.
func goQuote(text string) string {
	var out strings.Builder
	out.WriteByte('"')
	for _, r := range text {
		switch {
		case r == '"':
			out.WriteString(`\"`)
		case r == '\\':
			out.WriteString(`\\`)
		case r == '\n':
			out.WriteString(`\n`)
		case r == '\r':
			out.WriteString(`\r`)
		case r == '\t':
			out.WriteString(`\t`)
		case unicode.IsControl(r):
			fmt.Fprintf(&out, "\\u%04x", r)
		default:
			out.WriteRune(r)
		}
	}
	out.WriteByte('"')
	return out.String()
}
