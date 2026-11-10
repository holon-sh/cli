// holon-generated-apps-v1 app=chéck"out
package holon

import "encoding/json"

type ForXInput = map[string]json.RawMessage

type ForXOutput = json.RawMessage

type OrdersValueInput = map[string]json.RawMessage

type OrdersValueOutput = json.RawMessage

type OrdersX123Input = map[string]json.RawMessage

type OrdersX123Output = json.RawMessage

type OrdersCallTool2Input = map[string]json.RawMessage

type OrdersCallTool2Output = json.RawMessage

type OrdersHiddenInput = map[string]json.RawMessage

type OrdersHiddenOutput = json.RawMessage

type OrdersCallTool3Input = struct {
	A *json.Number "json:\"a,omitempty\""
	B string "json:\"b\""
}

type OrdersCallTool3Output = struct {
	X bool "json:\"x\""
}

type OrdersClassInput = struct {
	Opt *struct {
	N *json.Number "json:\"n,omitempty\""
} "json:\"opt,omitempty\""
}

type OrdersClassOutput = []json.RawMessage

type OrdersInputId = struct {
	Id string "json:\"id\""
}

type OrdersGetOrderInput = OrdersInputId

type OrdersGetOrderOutput = json.RawMessage

type OrdersGetOrder2Input = OrdersInputId

type OrdersGetOrder2Output = json.RawMessage

type OrdersInputX1startsDashKeyAllEtc struct {
	X1starts *string "json:\"1starts,omitempty\""
	DashKey *string "json:\"Dash-Key,omitempty\""
	All json.RawMessage "json:\"all,omitempty\""
	AllConflict json.RawMessage "json:\"allConflict,omitempty\""
	AllNonObject json.RawMessage "json:\"allNonObject,omitempty\""
	Annotated *string "json:\"annotated,omitempty\""
	ArrayRef *string "json:\"arrayRef,omitempty\""
	ClosedEmpty *map[string]json.RawMessage "json:\"closedEmpty,omitempty\""
	ConstNum *json.Number "json:\"constNum,omitempty\""
	ConstObj json.RawMessage "json:\"constObj,omitempty\""
	ConstString *string "json:\"constString,omitempty\""
	Cycle *OrdersInputNode "json:\"cycle,omitempty\""
	DashKey2 *string "json:\"dash-key,omitempty\""
	EmptyAll json.RawMessage "json:\"emptyAll,omitempty\""
	EmptyAny json.RawMessage "json:\"emptyAny,omitempty\""
	FalseSchema json.RawMessage "json:\"falseSchema,omitempty\""
	Files *[]struct {
	Data *string "json:\"data,omitempty\""
	Id string "json:\"id\""
	MimeType string "json:\"mimeType\""
	Name *string "json:\"name,omitempty\""
	Size json.Number "json:\"size\""
} "json:\"files,omitempty\""
	ImplicitTuple json.RawMessage "json:\"implicitTuple,omitempty\""
	Literals json.RawMessage "json:\"literals\""
	MapOf *map[string]string "json:\"mapOf,omitempty\""
	Multi json.RawMessage "json:\"multi,omitempty\""
	Nested *[][]struct {
	Deep json.RawMessage "json:\"deep,omitempty\""
} "json:\"nested,omitempty\""
	NoType struct {
	P bool "json:\"p\""
} "json:\"noType\""
	Nodes *OrdersInputNodes "json:\"nodes,omitempty\""
	NumericType *struct {
	P *bool "json:\"p,omitempty\""
} "json:\"numericType,omitempty\""
	OddKeyWithSpaces *string "json:\"odd key with spaces,omitempty\""
	OneOf json.RawMessage "json:\"oneOf,omitempty\""
	OpenObj *map[string]json.RawMessage "json:\"openObj,omitempty\""
	OpenTuple json.RawMessage "json:\"openTuple,omitempty\""
	PlainArray *[]json.RawMessage "json:\"plainArray,omitempty\""
	PointerEscape *OrdersInputABC "json:\"pointerEscape,omitempty\""
	RootRef *OrdersInputX1startsDashKeyAllEtc "json:\"rootRef,omitempty\""
	TrueObj *map[string]json.RawMessage "json:\"trueObj,omitempty\""
	TrueSchema json.RawMessage "json:\"trueSchema,omitempty\""
	Tuple json.RawMessage "json:\"tuple\""
	UnknownKeyword json.RawMessage "json:\"unknownKeyword,omitempty\""
}

type OrdersInputNode struct {
	Next *OrdersInputNode "json:\"next,omitempty\""
	Value json.Number "json:\"value\""
}

type OrdersInputNodes = []OrdersInputNode

type OrdersInputABC = json.Number

type OrdersInputX9lives = string

type OrdersInputLegacy = json.Number

type OrdersInputPrivate = bool

type OrdersInputFor = string

type OrdersInputList = json.RawMessage

type OrdersKitchenSinkInput = OrdersInputX1startsDashKeyAllEtc

type OrdersKitchenSinkOutput = json.RawMessage

type OrdersOrderListInput = map[string]json.RawMessage

type OrdersOrderListOutput = json.RawMessage

type OrdersOrderList2Input = map[string]json.RawMessage

type OrdersOrderList2Output = json.RawMessage

type OrdersRuntimeRunInput = struct {
	Code string "json:\"code\""
}

type OrdersRuntimeRunOutput = struct {
	Stdout string "json:\"stdout\""
}

type OrdersWeirdKeysInput = map[string]json.RawMessage

type OrdersWeirdKeysOutput = map[string]json.RawMessage

type OrdersMojiInput = map[string]json.RawMessage

type OrdersMojiOutput = string

type Orders2KitchenSinkInput = struct {
	Id string "json:\"id\""
}

type Orders2KitchenSinkOutput = bool

type WithHyphenYInput = map[string]json.RawMessage

type WithHyphenYOutput = json.RawMessage

type NZInput = map[string]json.RawMessage

type NZOutput = json.RawMessage

