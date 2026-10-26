package mcptype

import (
	"fmt"
	"reflect"
)

// change is one way a published contract breaks an expectation.
type change struct {
	kind                string
	name, uri           string
	expected, published Identity
}

func (c change) text() string {
	switch c.kind {
	case "no_contract":
		return "no published contract"
	case "identity":
		return fmt.Sprintf("identity %s@%s expected, %s@%s published", c.expected.Name, c.expected.Version, c.published.Name, c.published.Version)
	case "tool_removed":
		return fmt.Sprintf("tool %s removed", c.name)
	case "tool_input_changed":
		return fmt.Sprintf("tool %s input changed", c.name)
	case "tool_output_changed":
		return fmt.Sprintf("tool %s output changed", c.name)
	case "resource_removed":
		return fmt.Sprintf("resource %s removed", c.uri)
	case "resource_output_changed":
		return fmt.Sprintf("resource %s contents changed", c.uri)
	}
	panic("mcptype: unknown change kind " + c.kind)
}

// record is the change's JSON form: {"kind", "name" | "uri" | "expected" + "published", "text"}.
func (c change) record() Object {
	record := Object{"kind": c.kind, "text": c.text()}
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
		return []change{{kind: "no_contract"}}
	}
	var result []change
	if expected.Identity != published.Identity {
		result = append(result, change{kind: "identity", expected: expected.Identity, published: published.Identity})
	}
	want, have := canonicalContract(expected.Contract), canonicalContract(*published.Contract)
	for _, name := range sortedKeys(want.Tools) {
		tool := want.Tools[name]
		actual, ok := have.Tools[name]
		switch {
		case !ok:
			result = append(result, change{kind: "tool_removed", name: name})
		case !reflect.DeepEqual(actual.Input, tool.Input):
			result = append(result, change{kind: "tool_input_changed", name: name})
		case !reflect.DeepEqual(actual.Output, tool.Output):
			result = append(result, change{kind: "tool_output_changed", name: name})
		}
	}
	for _, uri := range sortedKeys(want.Resources) {
		actual, ok := have.Resources[uri]
		switch {
		case !ok:
			result = append(result, change{kind: "resource_removed", uri: uri})
		case !reflect.DeepEqual(actual.Output, want.Resources[uri].Output):
			result = append(result, change{kind: "resource_output_changed", uri: uri})
		}
	}
	return result
}
