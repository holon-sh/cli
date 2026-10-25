package holon

import (
	"context"

	holon "github.com/holon-sh/sdk/go"
)

type _dependency = holon.Dependency

var Orders = _Orders{&holon.Dependency{Name: "orders", Tools: map[string]holon.ToolSchema{
	"lookup": {
		Input:  "{\"additionalProperties\":false,\"properties\":{\"id\":{\"type\":\"string\"}},\"required\":[\"id\"],\"type\":\"object\"}",
		Output: "{\"additionalProperties\":false,\"properties\":{\"total\":{\"type\":\"number\"}},\"required\":[\"total\"],\"type\":\"object\"}",
	},
}}}

type _Orders struct{ *_dependency }

func (client _Orders) Lookup(ctx context.Context, input OrdersLookupInput) (OrdersLookupOutput, error) {
	return holon.Call[OrdersLookupOutput](ctx, client._dependency, "lookup", input)
}
