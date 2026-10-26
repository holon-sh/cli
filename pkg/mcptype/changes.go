package mcptype

import (
	"fmt"
	"reflect"
)

// changes lists what breaks expected if published is deployed. Empty means compatible.
// Identity first, then tools by name, then resources by URI.
func changes(expected *metadata, published *metadata) []Change {
	if published == nil || published.Schema == nil {
		return []Change{{Kind: "no_contract", Text: "no published contract"}}
	}
	var result []Change
	if expected.Identity != published.Identity {
		result = append(result, Change{
			Kind: "identity",
			Text: fmt.Sprintf("identity %s@%s expected, %s@%s published", expected.Identity.Name, expected.Identity.Version, published.Identity.Name, published.Identity.Version),
		})
	}
	want, have := canonicalContract(*expected.Schema), canonicalContract(*published.Schema)
	for _, name := range sortedKeys(want.Tools) {
		tool := want.Tools[name]
		actual, ok := have.Tools[name]
		switch {
		case !ok:
			result = append(result, Change{Kind: "tool_removed", Name: name, Text: fmt.Sprintf("tool %s removed", name)})
		case !reflect.DeepEqual(actual.Input, tool.Input):
			result = append(result, Change{Kind: "tool_input_changed", Name: name, Text: fmt.Sprintf("tool %s input changed", name)})
		case !reflect.DeepEqual(actual.Output, tool.Output):
			result = append(result, Change{Kind: "tool_output_changed", Name: name, Text: fmt.Sprintf("tool %s output changed", name)})
		}
	}
	for _, uri := range sortedKeys(want.Resources) {
		actual, ok := have.Resources[uri]
		switch {
		case !ok:
			result = append(result, Change{Kind: "resource_removed", Name: uri, Text: fmt.Sprintf("resource %s removed", uri)})
		case !reflect.DeepEqual(actual.Output, want.Resources[uri].Output):
			result = append(result, Change{Kind: "resource_output_changed", Name: uri, Text: fmt.Sprintf("resource %s contents changed", uri)})
		}
	}
	return result
}
