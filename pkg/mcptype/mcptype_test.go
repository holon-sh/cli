package mcptype

import (
	"encoding/json"
	"testing"

	"github.com/holon-sh/cli/pkg/mcpcontract"
)

func TestChanges(t *testing.T) {
	// The published input reorders unordered keywords and changes annotations at every depth.
	const input = `{"type":"object","properties":{"id":{"type":["string","null"],"enum":["a","b",null]},"value":{"anyOf":[{"type":"string","description":"old"},{"type":"number"}]}},"dependentSchemas":{"id":{"properties":{"value":{"description":"old","type":"string"}}}},"not":{"properties":{"blocked":{"type":"boolean","title":"old"}}}}`
	const equivalent = `{"type":"object","properties":{"id":{"type":["null","string"],"enum":[null,"b","a"]},"value":{"anyOf":[{"description":"new","type":"string"},{"type":"number"}]}},"dependentSchemas":{"id":{"properties":{"value":{"type":"string","description":"new"}}}},"not":{"properties":{"blocked":{"title":"new","type":"boolean"}}}}`
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
		changes   int
	}{
		{"equivalent schemas and additions", published("1", []mcpcontract.Tool{{Name: get.Name, Input: json.RawMessage(equivalent), Output: get.Output}, {Name: "new", Input: get.Input}}, summary, mcpcontract.Resource{URI: "orders://new", Output: summary.Output}), 0},
		{"identity, a tool and a resource", published("2", nil, mcpcontract.Resource{URI: summary.URI, Output: json.RawMessage(`{"type":"array"}`)}), 3},
		{"tool input", published("1", []mcpcontract.Tool{{Name: get.Name, Input: json.RawMessage(`{"type":"object"}`), Output: get.Output}}, summary), 1},
		{"tool output removed", published("1", []mcpcontract.Tool{{Name: get.Name, Input: get.Input}}, summary), 1},
		{"resource removed", published("1", []mcpcontract.Tool{get}), 1},
		{"no published contract", nil, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := Changes(expected, test.published)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != test.changes {
				t.Fatalf("changes = %v, want %d", got, test.changes)
			}
		})
	}
}
