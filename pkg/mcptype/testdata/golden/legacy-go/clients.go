package holon

import (
	"context"

	holon "example.com/runtime"
)

type _dependency = holon.Dependency

var Empty = _Empty{&holon.Dependency{Name: "empty", Tools: map[string]holon.ToolSchema{}}}

type _Empty struct{ *_dependency }

var Mysql = _Mysql{&holon.Dependency{Name: "mysql", Tools: map[string]holon.ToolSchema{
	"select": {
		Input:  "{\"$defs\":{\"Direction\":{\"enum\":[\"asc\",\"desc\"]},\"LabeledColumn\":{\"additionalProperties\":false,\"properties\":{\"column\":{\"type\":\"string\"},\"label\":{\"type\":\"string\"}},\"required\":[\"column\",\"label\"],\"type\":\"object\"}},\"additionalProperties\":false,\"properties\":{\"columns\":{\"anyOf\":[{\"items\":{\"type\":\"string\"},\"type\":\"array\"},{\"items\":{\"$ref\":\"#/$defs/LabeledColumn\"},\"type\":\"array\"},{\"type\":\"null\"}]},\"direction\":{\"$ref\":\"#/$defs/Direction\"},\"table\":{\"type\":\"string\"}},\"required\":[\"table\"],\"type\":\"object\"}",
		Output: "{\"$ref\":\"#/definitions/Root\",\"definitions\":{\"LabeledColumn\":{\"additionalProperties\":false,\"properties\":{\"value\":{\"type\":\"number\"}},\"required\":[\"value\"],\"type\":\"object\"},\"Root\":{\"additionalProperties\":false,\"properties\":{\"columns\":{\"items\":{\"$ref\":\"#/definitions/LabeledColumn\"},\"type\":\"array\"}},\"required\":[\"columns\"],\"type\":\"object\"}}}",
	},
}}}

type _Mysql struct{ *_dependency }

func (client _Mysql) Select(ctx context.Context, input MysqlSelectInput) (MysqlSelectOutput, error) {
	return holon.Call[MysqlSelectOutput](ctx, client._dependency, "select", input)
}

var Orders = _Orders{&holon.Dependency{Name: "orders", Tools: map[string]holon.ToolSchema{
	"get_order": {
		Input:  "{\"additionalProperties\":false,\"properties\":{\"count\":{\"type\":\"integer\"},\"orderId\":{\"type\":\"string\"}},\"required\":[\"orderId\"],\"type\":\"object\"}",
		Output: "{\"properties\":{\"total\":{\"type\":\"number\"}},\"required\":[\"total\"],\"type\":\"object\"}",
	},
	"then": {
		Input:  "{\"additionalProperties\":false,\"type\":\"object\"}",
		Output: "",
	},
}}}

type _Orders struct{ *_dependency }

func (client _Orders) GetOrder(ctx context.Context, input OrdersGetOrderInput) (OrdersGetOrderOutput, error) {
	return holon.Call[OrdersGetOrderOutput](ctx, client._dependency, "get_order", input)
}

func (client _Orders) Then(ctx context.Context, input OrdersThenInput) (OrdersThenOutput, error) {
	return holon.Call[OrdersThenOutput](ctx, client._dependency, "then", input)
}

var WithHyphen = _WithHyphen{&holon.Dependency{Name: "with-hyphen", Tools: map[string]holon.ToolSchema{
	"get_order": {
		Input:  "{\"additionalProperties\":false,\"properties\":{\"count\":{\"type\":\"integer\"},\"orderId\":{\"type\":\"string\"}},\"required\":[\"orderId\"],\"type\":\"object\"}",
		Output: "{\"properties\":{\"total\":{\"type\":\"number\"}},\"required\":[\"total\"],\"type\":\"object\"}",
	},
	"then": {
		Input:  "{\"additionalProperties\":false,\"type\":\"object\"}",
		Output: "",
	},
}}}

type _WithHyphen struct{ *_dependency }

func (client _WithHyphen) GetOrder(ctx context.Context, input WithHyphenGetOrderInput) (WithHyphenGetOrderOutput, error) {
	return holon.Call[WithHyphenGetOrderOutput](ctx, client._dependency, "get_order", input)
}

func (client _WithHyphen) Then(ctx context.Context, input WithHyphenThenInput) (WithHyphenThenOutput, error) {
	return holon.Call[WithHyphenThenOutput](ctx, client._dependency, "then", input)
}
