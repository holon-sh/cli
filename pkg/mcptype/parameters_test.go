package mcptype

import (
	"context"
	"slices"
	"testing"
)

func TestParametersReadReferencesAndNullableStrings(t *testing.T) {
	schema := `{"type":"object","required":["id"],"properties":{
		"id":{"type":"string"},
		"count":{"type":"integer"},
		"note":{"anyOf":[{"type":"string"},{"type":"null"}]},
		"kind":{"enum":["a","b"]},
		"item":{"$ref":"#/$defs/Item"},
		"tree":{"$ref":"#/$defs/Tree"}},
		"$defs":{"Item":{"type":"object","properties":{"sku":{"type":"string"}}},
			"Tree":{"type":"object","properties":{"children":{"type":"array","items":{"$ref":"#/$defs/Tree"}}}}}}`
	got, err := Parameters(context.Background(), []byte(schema))
	if err != nil {
		t.Fatal(err)
	}
	want := []Parameter{
		{Name: "count", Type: "number"},
		{Name: "id", Required: true, Type: "string", Text: true},
		{Name: "item", Type: `{ "sku"?: string; }`},
		{Name: "kind", Type: `("a" | "b")`, Text: true},
		{Name: "note", Type: "(string | null)", Text: true},
		{Name: "tree", Type: "Tree"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("Parameters =\n%+v\nwant\n%+v", got, want)
	}
}
