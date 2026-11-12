package mcptype

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/holon-sh/cli/pkg/mcpcontract"
)

func TestChanges(t *testing.T) {
	// The published input reorders unordered keywords and changes annotations at every depth.
	const input = `{"type":"object","properties":{"id":{"type":["string","null"],"enum":["a","b",null]},"value":{"anyOf":[{"type":"string","description":"old"},{"type":"number"}]}},"dependentSchemas":{"id":{"properties":{"value":{"description":"old","type":"string"}}}},"not":{"properties":{"blocked":{"type":"boolean","title":"old"}}}}`
	const equivalent = `{"type":"object","properties":{"id":{"type":["null","string"],"enum":[null,"b","a"]},"value":{"anyOf":[{"type":"number"},{"description":"new","type":"string"}]}},"dependentSchemas":{"id":{"properties":{"value":{"type":"string","description":"new"}}}},"not":{"properties":{"blocked":{"title":"new","type":"boolean"}}}}`
	get := mcpcontract.Tool{Name: "get", Input: json.RawMessage(input), Output: json.RawMessage(`{"type":"number"}`)}
	summary := mcpcontract.Resource{URI: "orders://summary", Output: json.RawMessage(`{"type":"object"}`)}
	identity := mcpcontract.Identity{Name: "example/orders", Version: "1"}
	expected := &mcpcontract.Metadata{Identity: identity, Schema: &mcpcontract.Contract{Tools: []mcpcontract.Tool{get}, Resources: []mcpcontract.Resource{summary}}}
	published := func(version string, tools []mcpcontract.Tool, resources ...mcpcontract.Resource) *mcpcontract.Metadata {
		return &mcpcontract.Metadata{Identity: mcpcontract.Identity{Name: identity.Name, Version: version}, Schema: &mcpcontract.Contract{Tools: tools, Resources: resources}}
	}
	for _, test := range []struct {
		name      string
		published *mcpcontract.Metadata
		want      []string
	}{
		{"equivalent schemas and additions", published("1", []mcpcontract.Tool{{Name: get.Name, Input: json.RawMessage(equivalent), Output: get.Output}, {Name: "new", Input: get.Input}}, summary, mcpcontract.Resource{URI: "orders://new", Output: summary.Output}), nil},
		{"identity, then tools, then resources", published("2", nil, mcpcontract.Resource{URI: summary.URI, Output: json.RawMessage(`{"type":"array"}`)}), []string{"identity example/orders@1 expected, example/orders@2 published", "tool get removed", "resource orders://summary contents changed"}},
		{"tool input", published("1", []mcpcontract.Tool{{Name: get.Name, Input: json.RawMessage(`{"type":"object"}`), Output: get.Output}}, summary), []string{"tool get input changed"}},
		{"tool output removed", published("1", []mcpcontract.Tool{{Name: get.Name, Input: get.Input}}, summary), []string{"tool get output changed"}},
		{"resource removed", published("1", []mcpcontract.Tool{get}), []string{"resource orders://summary removed"}},
		{"no published contract", nil, []string{"no published contract"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := Changes(expected, test.published)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, test.want) {
				t.Fatalf("changes = %v, want %v", got, test.want)
			}
		})
	}
	if _, err := Changes(expected, published("", nil)); err == nil {
		t.Fatal("invalid published metadata accepted")
	}
}
