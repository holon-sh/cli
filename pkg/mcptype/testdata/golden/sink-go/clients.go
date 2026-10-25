// holon-generated-apps-v1 app=chéck"out
package holon

import (
	"context"

	holon "example.com/é/runtime"
)

type _dependency = holon.Dependency

var X123 = _X123{&holon.Dependency{Name: "123", Tools: map[string]holon.ToolSchema{}}}

type _X123 struct{ *_dependency }

var Private = _Private{&holon.Dependency{Name: "_private", Tools: map[string]holon.ToolSchema{}}}

type _Private struct{ *_dependency }

var For = _For{&holon.Dependency{Name: "for", Tools: map[string]holon.ToolSchema{
	"x": {
		Input:  "{\"type\":\"object\"}",
		Output: "",
	},
}}}

type _For struct{ *_dependency }

func (client _For) X(ctx context.Context, input ForXInput) (ForXOutput, error) {
	return holon.Call[ForXOutput](ctx, client._dependency, "x", input)
}

var Orders = _Orders{&holon.Dependency{Name: "orders", Tools: map[string]holon.ToolSchema{
	"-": {
		Input:  "{\"type\":\"object\"}",
		Output: "",
	},
	"123": {
		Input:  "{\"type\":\"object\"}",
		Output: "",
	},
	"CallTool": {
		Input:  "{\"type\":\"object\"}",
		Output: "",
	},
	"call_tool": {
		Input:  "{\"additionalProperties\":false,\"properties\":{\"a\":{\"type\":\"integer\"},\"b\":{\"type\":\"string\"}},\"required\":[\"b\"],\"type\":\"object\"}",
		Output: "{\"additionalProperties\":false,\"properties\":{\"x\":{\"type\":\"boolean\"}},\"required\":[\"x\"],\"type\":\"object\"}",
	},
	"class": {
		Input:  "{\"additionalProperties\":false,\"properties\":{\"opt\":{\"additionalProperties\":false,\"properties\":{\"n\":{\"type\":\"number\"}},\"type\":\"object\"}},\"type\":\"object\"}",
		Output: "{\"items\":{\"$ref\":\"#/items\"},\"type\":\"array\"}",
	},
	"get-order": {
		Input:  "{\"additionalProperties\":false,\"properties\":{\"id\":{\"type\":\"string\"}},\"required\":[\"id\"],\"type\":\"object\"}",
		Output: "",
	},
	"get_order": {
		Input:  "{\"additionalProperties\":false,\"properties\":{\"id\":{\"type\":\"string\"}},\"required\":[\"id\"],\"type\":\"object\"}",
		Output: "",
	},
	"kitchen-sink": {
		Input:  "{\"$defs\":{\"9lives\":{\"type\":\"string\"},\"Node\":{\"additionalProperties\":false,\"properties\":{\"next\":{\"$ref\":\"#/$defs/Node\"},\"value\":{\"type\":\"integer\"}},\"required\":[\"value\"],\"type\":\"object\"},\"_private\":{\"type\":\"boolean\"},\"a/b~c\":{\"type\":\"integer\"},\"for\":{\"type\":\"string\"},\"list\":{\"prefixItems\":[{\"type\":\"string\"}],\"type\":\"array\"}},\"additionalProperties\":false,\"definitions\":{\"Legacy\":{\"type\":\"number\"},\"Node\":{\"type\":\"string\"}},\"properties\":{\"1starts\":{\"type\":\"string\"},\"Dash-Key\":{\"type\":\"string\"},\"all\":{\"allOf\":[{\"properties\":{\"a\":{\"type\":\"string\"}},\"required\":[\"a\"],\"type\":\"object\"},{\"properties\":{\"b\":{\"type\":\"integer\"}},\"type\":\"object\"}]},\"allConflict\":{\"allOf\":[{\"properties\":{\"a\":{\"type\":\"string\"}},\"type\":\"object\"},{\"properties\":{\"a\":{\"type\":\"integer\"}},\"type\":\"object\"}]},\"allNonObject\":{\"allOf\":[{\"type\":\"string\"},{\"type\":\"object\"}]},\"annotated\":{\"$comment\":\"c\",\"description\":\"gone\",\"examples\":[1],\"title\":\"t\",\"type\":\"string\"},\"arrayRef\":{\"$ref\":\"#/$defs/list/prefixItems/0\"},\"closedEmpty\":{\"additionalProperties\":false,\"type\":\"object\"},\"constNum\":{\"const\":1e+21},\"constObj\":{\"const\":{\"deep\":[1,2]}},\"constString\":{\"const\":\"it's \\\"quoted\\\"\\n\"},\"cycle\":{\"$ref\":\"#/$defs/Node\"},\"dash-key\":{\"type\":\"string\"},\"emptyAll\":{\"allOf\":[]},\"emptyAny\":{\"anyOf\":[]},\"falseSchema\":false,\"implicitTuple\":{\"minItems\":2.0,\"prefixItems\":[{\"const\":\"x\"}],\"type\":\"array\"},\"literals\":{\"enum\":[\"a\",1,2.5,-3,true,null,[1],{\"k\":\"v\"},1.0,1e+21,-0.0]},\"mapOf\":{\"additionalProperties\":{\"type\":\"string\"},\"type\":\"object\"},\"multi\":{\"items\":{\"type\":\"integer\"},\"type\":[\"string\",\"null\",\"array\",5]},\"nested\":{\"items\":{\"items\":{\"additionalProperties\":false,\"properties\":{\"deep\":{\"type\":\"null\"}},\"type\":\"object\"},\"type\":\"array\"},\"type\":\"array\"},\"noType\":{\"properties\":{\"p\":{\"type\":\"boolean\"}},\"required\":[\"p\",7]},\"numericType\":{\"properties\":{\"p\":{\"type\":\"boolean\"}},\"type\":7},\"odd key with spaces\":{\"type\":\"string\"},\"oneOf\":{\"oneOf\":[{\"type\":\"string\"},{\"type\":\"number\"}]},\"openObj\":{\"additionalProperties\":{\"type\":\"integer\"},\"properties\":{\"x\":{\"type\":\"string\"}},\"type\":\"object\"},\"openTuple\":{\"additionalItems\":{\"type\":\"number\"},\"items\":[{\"type\":\"boolean\"}],\"type\":\"array\"},\"plainArray\":{\"type\":\"array\"},\"pointerEscape\":{\"$ref\":\"#/$defs/a~1b~0c\"},\"rootRef\":{\"$ref\":\"#\"},\"trueObj\":{\"additionalProperties\":true,\"properties\":{\"x\":{\"type\":\"string\"}},\"type\":\"object\"},\"trueSchema\":true,\"tuple\":{\"items\":false,\"minItems\":1,\"prefixItems\":[{\"type\":\"string\"},{\"type\":\"integer\"}],\"type\":\"array\"},\"unknownKeyword\":{\"format\":\"email\"}},\"required\":[\"tuple\",\"literals\",\"noType\"],\"type\":\"object\"}",
		Output: "",
	},
	"weird-keys": {
		Input:  "{\"additionalProperties\":false,\"properties\":{\"\":{\"type\":\"null\"},\"-\":{\"type\":\"boolean\"},\"x\\\"quote\":{\"type\":\"string\"},\"ünïcode\":{\"type\":\"integer\"}},\"required\":[\"\"],\"type\":\"object\"}",
		Output: "{\"additionalProperties\":false,\"properties\":{\"a.b\":{\"type\":\"string\"},\"nl\\nname\":{\"type\":\"string\"},\"tab\\tname\":{\"type\":\"string\"}},\"required\":[\"tab\\tname\"],\"type\":\"object\"}",
	},
	"émoji🎉": {
		Input:  "{\"properties\":{\"é\":{\"type\":\"string\"}},\"type\":\"object\"}",
		Output: "{\"description\":\"é\",\"type\":\"string\"}",
	},
}}}

type _Orders struct{ *_dependency }

func (client _Orders) Value(ctx context.Context, input OrdersValueInput) (OrdersValueOutput, error) {
	return holon.Call[OrdersValueOutput](ctx, client._dependency, "-", input)
}

func (client _Orders) X123(ctx context.Context, input OrdersX123Input) (OrdersX123Output, error) {
	return holon.Call[OrdersX123Output](ctx, client._dependency, "123", input)
}

func (client _Orders) CallTool2(ctx context.Context, input OrdersCallTool2Input) (OrdersCallTool2Output, error) {
	return holon.Call[OrdersCallTool2Output](ctx, client._dependency, "CallTool", input)
}

func (client _Orders) CallTool3(ctx context.Context, input OrdersCallTool3Input) (OrdersCallTool3Output, error) {
	return holon.Call[OrdersCallTool3Output](ctx, client._dependency, "call_tool", input)
}

func (client _Orders) Class(ctx context.Context, input OrdersClassInput) (OrdersClassOutput, error) {
	return holon.Call[OrdersClassOutput](ctx, client._dependency, "class", input)
}

func (client _Orders) GetOrder(ctx context.Context, input OrdersGetOrderInput) (OrdersGetOrderOutput, error) {
	return holon.Call[OrdersGetOrderOutput](ctx, client._dependency, "get-order", input)
}

func (client _Orders) GetOrder2(ctx context.Context, input OrdersGetOrder2Input) (OrdersGetOrder2Output, error) {
	return holon.Call[OrdersGetOrder2Output](ctx, client._dependency, "get_order", input)
}

func (client _Orders) KitchenSink(ctx context.Context, input OrdersKitchenSinkInput) (OrdersKitchenSinkOutput, error) {
	return holon.Call[OrdersKitchenSinkOutput](ctx, client._dependency, "kitchen-sink", input)
}

func (client _Orders) WeirdKeys(ctx context.Context, input OrdersWeirdKeysInput) (OrdersWeirdKeysOutput, error) {
	return holon.Call[OrdersWeirdKeysOutput](ctx, client._dependency, "weird-keys", input)
}

func (client _Orders) Moji(ctx context.Context, input OrdersMojiInput) (OrdersMojiOutput, error) {
	return holon.Call[OrdersMojiOutput](ctx, client._dependency, "émoji🎉", input)
}

var Orders2 = _Orders2{&holon.Dependency{Name: "orders!", Tools: map[string]holon.ToolSchema{
	"-": {
		Input:  "{\"type\":\"object\"}",
		Output: "",
	},
	"123": {
		Input:  "{\"type\":\"object\"}",
		Output: "",
	},
	"CallTool": {
		Input:  "{\"type\":\"object\"}",
		Output: "",
	},
	"call_tool": {
		Input:  "{\"additionalProperties\":false,\"properties\":{\"a\":{\"type\":\"integer\"},\"b\":{\"type\":\"string\"}},\"required\":[\"b\"],\"type\":\"object\"}",
		Output: "{\"additionalProperties\":false,\"properties\":{\"x\":{\"type\":\"boolean\"}},\"required\":[\"x\"],\"type\":\"object\"}",
	},
	"class": {
		Input:  "{\"additionalProperties\":false,\"properties\":{\"opt\":{\"additionalProperties\":false,\"properties\":{\"n\":{\"type\":\"number\"}},\"type\":\"object\"}},\"type\":\"object\"}",
		Output: "{\"items\":{\"$ref\":\"#/items\"},\"type\":\"array\"}",
	},
	"get-order": {
		Input:  "{\"additionalProperties\":false,\"properties\":{\"id\":{\"type\":\"string\"}},\"required\":[\"id\"],\"type\":\"object\"}",
		Output: "",
	},
	"get_order": {
		Input:  "{\"additionalProperties\":false,\"properties\":{\"id\":{\"type\":\"string\"}},\"required\":[\"id\"],\"type\":\"object\"}",
		Output: "",
	},
	"kitchen-sink": {
		Input:  "{\"$defs\":{\"9lives\":{\"type\":\"string\"},\"Node\":{\"additionalProperties\":false,\"properties\":{\"next\":{\"$ref\":\"#/$defs/Node\"},\"value\":{\"type\":\"integer\"}},\"required\":[\"value\"],\"type\":\"object\"},\"_private\":{\"type\":\"boolean\"},\"a/b~c\":{\"type\":\"integer\"},\"for\":{\"type\":\"string\"},\"list\":{\"prefixItems\":[{\"type\":\"string\"}],\"type\":\"array\"}},\"additionalProperties\":false,\"definitions\":{\"Legacy\":{\"type\":\"number\"},\"Node\":{\"type\":\"string\"}},\"properties\":{\"1starts\":{\"type\":\"string\"},\"Dash-Key\":{\"type\":\"string\"},\"all\":{\"allOf\":[{\"properties\":{\"a\":{\"type\":\"string\"}},\"required\":[\"a\"],\"type\":\"object\"},{\"properties\":{\"b\":{\"type\":\"integer\"}},\"type\":\"object\"}]},\"allConflict\":{\"allOf\":[{\"properties\":{\"a\":{\"type\":\"string\"}},\"type\":\"object\"},{\"properties\":{\"a\":{\"type\":\"integer\"}},\"type\":\"object\"}]},\"allNonObject\":{\"allOf\":[{\"type\":\"string\"},{\"type\":\"object\"}]},\"annotated\":{\"$comment\":\"c\",\"description\":\"gone\",\"examples\":[1],\"title\":\"t\",\"type\":\"string\"},\"arrayRef\":{\"$ref\":\"#/$defs/list/prefixItems/0\"},\"closedEmpty\":{\"additionalProperties\":false,\"type\":\"object\"},\"constNum\":{\"const\":1e+21},\"constObj\":{\"const\":{\"deep\":[1,2]}},\"constString\":{\"const\":\"it's \\\"quoted\\\"\\n\"},\"cycle\":{\"$ref\":\"#/$defs/Node\"},\"dash-key\":{\"type\":\"string\"},\"emptyAll\":{\"allOf\":[]},\"emptyAny\":{\"anyOf\":[]},\"falseSchema\":false,\"implicitTuple\":{\"minItems\":2.0,\"prefixItems\":[{\"const\":\"x\"}],\"type\":\"array\"},\"literals\":{\"enum\":[\"a\",1,2.5,-3,true,null,[1],{\"k\":\"v\"},1.0,1e+21,-0.0]},\"mapOf\":{\"additionalProperties\":{\"type\":\"string\"},\"type\":\"object\"},\"multi\":{\"items\":{\"type\":\"integer\"},\"type\":[\"string\",\"null\",\"array\",5]},\"nested\":{\"items\":{\"items\":{\"additionalProperties\":false,\"properties\":{\"deep\":{\"type\":\"null\"}},\"type\":\"object\"},\"type\":\"array\"},\"type\":\"array\"},\"noType\":{\"properties\":{\"p\":{\"type\":\"boolean\"}},\"required\":[\"p\",7]},\"numericType\":{\"properties\":{\"p\":{\"type\":\"boolean\"}},\"type\":7},\"odd key with spaces\":{\"type\":\"string\"},\"oneOf\":{\"oneOf\":[{\"type\":\"string\"},{\"type\":\"number\"}]},\"openObj\":{\"additionalProperties\":{\"type\":\"integer\"},\"properties\":{\"x\":{\"type\":\"string\"}},\"type\":\"object\"},\"openTuple\":{\"additionalItems\":{\"type\":\"number\"},\"items\":[{\"type\":\"boolean\"}],\"type\":\"array\"},\"plainArray\":{\"type\":\"array\"},\"pointerEscape\":{\"$ref\":\"#/$defs/a~1b~0c\"},\"rootRef\":{\"$ref\":\"#\"},\"trueObj\":{\"additionalProperties\":true,\"properties\":{\"x\":{\"type\":\"string\"}},\"type\":\"object\"},\"trueSchema\":true,\"tuple\":{\"items\":false,\"minItems\":1,\"prefixItems\":[{\"type\":\"string\"},{\"type\":\"integer\"}],\"type\":\"array\"},\"unknownKeyword\":{\"format\":\"email\"}},\"required\":[\"tuple\",\"literals\",\"noType\"],\"type\":\"object\"}",
		Output: "",
	},
	"weird-keys": {
		Input:  "{\"additionalProperties\":false,\"properties\":{\"\":{\"type\":\"null\"},\"-\":{\"type\":\"boolean\"},\"x\\\"quote\":{\"type\":\"string\"},\"ünïcode\":{\"type\":\"integer\"}},\"required\":[\"\"],\"type\":\"object\"}",
		Output: "{\"additionalProperties\":false,\"properties\":{\"a.b\":{\"type\":\"string\"},\"nl\\nname\":{\"type\":\"string\"},\"tab\\tname\":{\"type\":\"string\"}},\"required\":[\"tab\\tname\"],\"type\":\"object\"}",
	},
	"émoji🎉": {
		Input:  "{\"properties\":{\"é\":{\"type\":\"string\"}},\"type\":\"object\"}",
		Output: "{\"description\":\"é\",\"type\":\"string\"}",
	},
}}}

type _Orders2 struct{ *_dependency }

func (client _Orders2) Value(ctx context.Context, input Orders2ValueInput) (Orders2ValueOutput, error) {
	return holon.Call[Orders2ValueOutput](ctx, client._dependency, "-", input)
}

func (client _Orders2) X123(ctx context.Context, input Orders2X123Input) (Orders2X123Output, error) {
	return holon.Call[Orders2X123Output](ctx, client._dependency, "123", input)
}

func (client _Orders2) CallTool2(ctx context.Context, input Orders2CallTool2Input) (Orders2CallTool2Output, error) {
	return holon.Call[Orders2CallTool2Output](ctx, client._dependency, "CallTool", input)
}

func (client _Orders2) CallTool3(ctx context.Context, input Orders2CallTool3Input) (Orders2CallTool3Output, error) {
	return holon.Call[Orders2CallTool3Output](ctx, client._dependency, "call_tool", input)
}

func (client _Orders2) Class(ctx context.Context, input Orders2ClassInput) (Orders2ClassOutput, error) {
	return holon.Call[Orders2ClassOutput](ctx, client._dependency, "class", input)
}

func (client _Orders2) GetOrder(ctx context.Context, input Orders2GetOrderInput) (Orders2GetOrderOutput, error) {
	return holon.Call[Orders2GetOrderOutput](ctx, client._dependency, "get-order", input)
}

func (client _Orders2) GetOrder2(ctx context.Context, input Orders2GetOrder2Input) (Orders2GetOrder2Output, error) {
	return holon.Call[Orders2GetOrder2Output](ctx, client._dependency, "get_order", input)
}

func (client _Orders2) KitchenSink(ctx context.Context, input Orders2KitchenSinkInput) (Orders2KitchenSinkOutput, error) {
	return holon.Call[Orders2KitchenSinkOutput](ctx, client._dependency, "kitchen-sink", input)
}

func (client _Orders2) WeirdKeys(ctx context.Context, input Orders2WeirdKeysInput) (Orders2WeirdKeysOutput, error) {
	return holon.Call[Orders2WeirdKeysOutput](ctx, client._dependency, "weird-keys", input)
}

func (client _Orders2) Moji(ctx context.Context, input Orders2MojiInput) (Orders2MojiOutput, error) {
	return holon.Call[Orders2MojiOutput](ctx, client._dependency, "émoji🎉", input)
}

var WithHyphen = _WithHyphen{&holon.Dependency{Name: "with-hyphen", Tools: map[string]holon.ToolSchema{
	"y": {
		Input:  "{\"type\":\"object\"}",
		Output: "",
	},
}}}

type _WithHyphen struct{ *_dependency }

func (client _WithHyphen) Y(ctx context.Context, input WithHyphenYInput) (WithHyphenYOutput, error) {
	return holon.Call[WithHyphenYOutput](ctx, client._dependency, "y", input)
}

var N = _N{&holon.Dependency{Name: "ünï", Tools: map[string]holon.ToolSchema{
	"z": {
		Input:  "{\"type\":\"object\"}",
		Output: "",
	},
}}}

type _N struct{ *_dependency }

func (client _N) Z(ctx context.Context, input NZInput) (NZOutput, error) {
	return holon.Call[NZOutput](ctx, client._dependency, "z", input)
}
