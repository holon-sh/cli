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

type OrdersGetOrderInput = struct {
	Id string "json:\"id\""
}

type OrdersGetOrderOutput = json.RawMessage

type OrdersGetOrder2Input = struct {
	Id string "json:\"id\""
}

type OrdersGetOrder2Output = json.RawMessage

type OrdersKitchenSinkInput = struct {
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
	Cycle *struct {
	Next json.RawMessage "json:\"next,omitempty\""
	Value json.Number "json:\"value\""
} "json:\"cycle,omitempty\""
	DashKey2 *string "json:\"dash-key,omitempty\""
	EmptyAll json.RawMessage "json:\"emptyAll,omitempty\""
	EmptyAny json.RawMessage "json:\"emptyAny,omitempty\""
	FalseSchema json.RawMessage "json:\"falseSchema,omitempty\""
	ImplicitTuple json.RawMessage "json:\"implicitTuple,omitempty\""
	Literals json.RawMessage "json:\"literals\""
	MapOf *map[string]string "json:\"mapOf,omitempty\""
	Multi json.RawMessage "json:\"multi,omitempty\""
	Nested *[][]struct {
	Deep json.RawMessage "json:\"deep,omitempty\""
} "json:\"nested,omitempty\""
	NoType map[string]json.RawMessage "json:\"noType\""
	NumericType *map[string]json.RawMessage "json:\"numericType,omitempty\""
	OddKeyWithSpaces *string "json:\"odd key with spaces,omitempty\""
	OneOf json.RawMessage "json:\"oneOf,omitempty\""
	OpenObj *map[string]json.RawMessage "json:\"openObj,omitempty\""
	OpenTuple json.RawMessage "json:\"openTuple,omitempty\""
	PlainArray *[]json.RawMessage "json:\"plainArray,omitempty\""
	PointerEscape *json.Number "json:\"pointerEscape,omitempty\""
	RootRef *struct {
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
	Cycle *struct {
	Next json.RawMessage "json:\"next,omitempty\""
	Value json.Number "json:\"value\""
} "json:\"cycle,omitempty\""
	DashKey2 *string "json:\"dash-key,omitempty\""
	EmptyAll json.RawMessage "json:\"emptyAll,omitempty\""
	EmptyAny json.RawMessage "json:\"emptyAny,omitempty\""
	FalseSchema json.RawMessage "json:\"falseSchema,omitempty\""
	ImplicitTuple json.RawMessage "json:\"implicitTuple,omitempty\""
	Literals json.RawMessage "json:\"literals\""
	MapOf *map[string]string "json:\"mapOf,omitempty\""
	Multi json.RawMessage "json:\"multi,omitempty\""
	Nested *[][]struct {
	Deep json.RawMessage "json:\"deep,omitempty\""
} "json:\"nested,omitempty\""
	NoType map[string]json.RawMessage "json:\"noType\""
	NumericType *map[string]json.RawMessage "json:\"numericType,omitempty\""
	OddKeyWithSpaces *string "json:\"odd key with spaces,omitempty\""
	OneOf json.RawMessage "json:\"oneOf,omitempty\""
	OpenObj *map[string]json.RawMessage "json:\"openObj,omitempty\""
	OpenTuple json.RawMessage "json:\"openTuple,omitempty\""
	PlainArray *[]json.RawMessage "json:\"plainArray,omitempty\""
	PointerEscape *json.Number "json:\"pointerEscape,omitempty\""
	RootRef json.RawMessage "json:\"rootRef,omitempty\""
	TrueObj *map[string]json.RawMessage "json:\"trueObj,omitempty\""
	TrueSchema json.RawMessage "json:\"trueSchema,omitempty\""
	Tuple json.RawMessage "json:\"tuple\""
	UnknownKeyword json.RawMessage "json:\"unknownKeyword,omitempty\""
} "json:\"rootRef,omitempty\""
	TrueObj *map[string]json.RawMessage "json:\"trueObj,omitempty\""
	TrueSchema json.RawMessage "json:\"trueSchema,omitempty\""
	Tuple json.RawMessage "json:\"tuple\""
	UnknownKeyword json.RawMessage "json:\"unknownKeyword,omitempty\""
}

type OrdersKitchenSinkInputX9lives = string

type OrdersKitchenSinkInputLegacy = json.Number

type OrdersKitchenSinkInputNode = struct {
	Next *struct {
	Next json.RawMessage "json:\"next,omitempty\""
	Value json.Number "json:\"value\""
} "json:\"next,omitempty\""
	Value json.Number "json:\"value\""
}

type OrdersKitchenSinkInputPrivate = bool

type OrdersKitchenSinkInputABC = json.Number

type OrdersKitchenSinkInputFor = string

type OrdersKitchenSinkInputList = json.RawMessage

type OrdersKitchenSinkOutput = json.RawMessage

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

