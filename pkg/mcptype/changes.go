package mcptype

import (
	"fmt"
	"reflect"
)

// change is one way a published contract breaks an expectation.
type change struct {
	kind                string
	name, uri, text     string
	expected, published Identity
}

// record is the change's JSON form: {"kind", "name" | "uri" | "expected" + "published", "text"}.
func (c change) record() Object {
	record := Object{"kind": c.kind, "text": c.text}
	switch c.kind {
	case "identity":
		record["expected"] = Object{"name": c.expected.Name, "version": c.expected.Version}
		record["published"] = Object{"name": c.published.Name, "version": c.published.Version}
	case "tool_removed", "tool_input_changed", "tool_output_changed":
		record["name"] = c.name
	case "resource_removed", "resource_output_changed":
		record["uri"] = c.uri
	}
	return record
}

// changes lists what breaks expected if published is deployed. Empty means compatible.
// Identity first, then tools by name, then resources by URI.
func changes(expected Expectation, published *Metadata) []change {
	if published == nil || published.Contract == nil {
		return []change{{kind: "no_contract", text: "no published contract"}}
	}
	var result []change
	if expected.Identity != published.Identity {
		result = append(result, change{
			kind: "identity", expected: expected.Identity, published: published.Identity,
			text: fmt.Sprintf("identity %s@%s expected, %s@%s published", expected.Identity.Name, expected.Identity.Version, published.Identity.Name, published.Identity.Version),
		})
	}
	want, have := canonicalContract(expected.Contract), canonicalContract(*published.Contract)
	for _, name := range sortedKeys(want.Tools) {
		tool := want.Tools[name]
		actual, ok := have.Tools[name]
		switch {
		case !ok:
			result = append(result, change{kind: "tool_removed", name: name, text: fmt.Sprintf("tool %s removed", name)})
		case !reflect.DeepEqual(actual.Input, tool.Input):
			result = append(result, change{kind: "tool_input_changed", name: name, text: fmt.Sprintf("tool %s input changed", name)})
		case !reflect.DeepEqual(actual.Output, tool.Output):
			result = append(result, change{kind: "tool_output_changed", name: name, text: fmt.Sprintf("tool %s output changed", name)})
		}
	}
	for _, uri := range sortedKeys(want.Resources) {
		actual, ok := have.Resources[uri]
		switch {
		case !ok:
			result = append(result, change{kind: "resource_removed", uri: uri, text: fmt.Sprintf("resource %s removed", uri)})
		case !reflect.DeepEqual(actual.Output, want.Resources[uri].Output):
			result = append(result, change{kind: "resource_output_changed", uri: uri, text: fmt.Sprintf("resource %s contents changed", uri)})
		}
	}
	return result
}
