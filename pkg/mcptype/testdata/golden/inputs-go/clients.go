package holon

import (
	"context"

	holon "example.com/runtime"
)

type _dependency = holon.Dependency

var Orders = _Orders{&holon.Dependency{Name: "orders", Tools: map[string]holon.ToolSchema{
	"lookup": {
		Input:  "{\"$defs\":{\"Row\":{\"properties\":{\"sku\":{\"type\":\"string\"}},\"required\":[\"sku\"],\"type\":\"object\"}},\"properties\":{\"byRegion\":{\"additionalProperties\":{\"properties\":{\"quantity\":{\"type\":\"integer\"}},\"type\":\"object\"},\"type\":\"object\"},\"id\":{\"type\":\"string\"},\"rows\":{\"items\":{\"$ref\":\"#/$defs/Row\"},\"type\":\"array\"}},\"required\":[\"id\",\"rows\",\"byRegion\"],\"type\":\"object\"}",
		Output: "{\"properties\":{\"total\":{\"type\":\"number\"}},\"type\":\"object\"}",
	},
}}}

type _Orders struct{ *_dependency }

func (client _Orders) Lookup(ctx context.Context, input OrdersLookupInput) (OrdersLookupOutput, error) {
	return holon.Call[OrdersLookupOutput](ctx, client._dependency, "lookup", input)
}
