# holon-generated-apps-v1 app=chéck"out
# Input alternatives share a result type; redundant overloads are harmless.
# mypy: disable-error-code=overload-cannot-match
from __future__ import annotations

import json as _json

from holon.client import Apps as _Apps
from holon.client import Binding as _Binding
from holon.client import CallOptions as _CallOptions
from holon.client import ToolResult as _ToolResult

import builtins as _builtins
import typing as _typing

OrdersCallToolInput = _typing.TypedDict("OrdersCallToolInput", {"a": _typing.NotRequired[_builtins.int], "b": _builtins.str})

OrdersCallToolOutput = _typing.TypedDict("OrdersCallToolOutput", {"x": _builtins.bool})

OrdersClassInputOpt = _typing.TypedDict("OrdersClassInputOpt", {"n": _typing.NotRequired[_builtins.float]})

OrdersClassInput = _typing.TypedDict("OrdersClassInput", {"opt": _typing.NotRequired[OrdersClassInputOpt]})

OrdersGetOrderInput = _typing.TypedDict("OrdersGetOrderInput", {"id": _builtins.str})

OrdersGetOrderInput2 = _typing.TypedDict("OrdersGetOrderInput2", {"id": _builtins.str})

OrdersKitchenSinkInputAll = _typing.TypedDict("OrdersKitchenSinkInputAll", {"a": _builtins.str, "b": _typing.NotRequired[_builtins.int]})

OrdersKitchenSinkInputClosedEmpty = _typing.TypedDict("OrdersKitchenSinkInputClosedEmpty", {})

OrdersKitchenSinkInputCycle = _typing.TypedDict("OrdersKitchenSinkInputCycle", {"next": _typing.NotRequired[_builtins.object], "value": _builtins.int})

OrdersKitchenSinkInputNestedItemItem = _typing.TypedDict("OrdersKitchenSinkInputNestedItemItem", {"deep": _typing.NotRequired[None]})

OrdersKitchenSinkInputNoType = _typing.TypedDict("OrdersKitchenSinkInputNoType", {"p": _builtins.bool})

OrdersKitchenSinkInputNumericType = _typing.TypedDict("OrdersKitchenSinkInputNumericType", {"p": _typing.NotRequired[_builtins.bool]})

OrdersKitchenSinkInputRootRefAll = _typing.TypedDict("OrdersKitchenSinkInputRootRefAll", {"a": _builtins.str, "b": _typing.NotRequired[_builtins.int]})

OrdersKitchenSinkInputRootRefClosedEmpty = _typing.TypedDict("OrdersKitchenSinkInputRootRefClosedEmpty", {})

OrdersKitchenSinkInputRootRefCycle = _typing.TypedDict("OrdersKitchenSinkInputRootRefCycle", {"next": _typing.NotRequired[_builtins.object], "value": _builtins.int})

OrdersKitchenSinkInputRootRefNestedItemItem = _typing.TypedDict("OrdersKitchenSinkInputRootRefNestedItemItem", {"deep": _typing.NotRequired[None]})

OrdersKitchenSinkInputRootRefNoType = _typing.TypedDict("OrdersKitchenSinkInputRootRefNoType", {"p": _builtins.bool})

OrdersKitchenSinkInputRootRefNumericType = _typing.TypedDict("OrdersKitchenSinkInputRootRefNumericType", {"p": _typing.NotRequired[_builtins.bool]})

OrdersKitchenSinkInputRootRef = _typing.TypedDict("OrdersKitchenSinkInputRootRef", {"1starts": _typing.NotRequired[_builtins.str], "Dash-Key": _typing.NotRequired[_builtins.str], "all": _typing.NotRequired[OrdersKitchenSinkInputRootRefAll], "allConflict": _typing.NotRequired[_builtins.object], "allNonObject": _typing.NotRequired[_builtins.object], "annotated": _typing.NotRequired[_builtins.str], "arrayRef": _typing.NotRequired[_builtins.str], "closedEmpty": _typing.NotRequired[OrdersKitchenSinkInputRootRefClosedEmpty], "constNum": _typing.NotRequired[_builtins.float], "constObj": _typing.NotRequired[_builtins.object], "constString": _typing.NotRequired[_typing.Literal["it's \"quoted\"\n"]], "cycle": _typing.NotRequired[OrdersKitchenSinkInputRootRefCycle], "dash-key": _typing.NotRequired[_builtins.str], "emptyAll": _typing.NotRequired[_builtins.dict[_builtins.str, _builtins.object]], "emptyAny": _typing.NotRequired[_typing.Never], "falseSchema": _typing.NotRequired[_typing.Never], "implicitTuple": _typing.NotRequired[_builtins.list[_builtins.object]], "literals": _typing.Literal["a"] | _typing.Literal[1] | _builtins.float | _typing.Literal[-3] | _typing.Literal[True] | None | _builtins.object | _builtins.object | _builtins.float | _builtins.float | _builtins.float, "mapOf": _typing.NotRequired[_builtins.dict[_builtins.str, _builtins.str]], "multi": _typing.NotRequired[_builtins.str | None | _builtins.list[_builtins.int] | _builtins.object], "nested": _typing.NotRequired[_builtins.list[_builtins.list[OrdersKitchenSinkInputRootRefNestedItemItem]]], "noType": OrdersKitchenSinkInputRootRefNoType, "numericType": _typing.NotRequired[OrdersKitchenSinkInputRootRefNumericType], "odd key with spaces": _typing.NotRequired[_builtins.str], "oneOf": _typing.NotRequired[_builtins.str | _builtins.float], "openObj": _typing.NotRequired[_builtins.dict[_builtins.str, _builtins.object]], "openTuple": _typing.NotRequired[_builtins.list[_builtins.object]], "plainArray": _typing.NotRequired[_builtins.list[_builtins.object]], "pointerEscape": _typing.NotRequired[_builtins.int], "rootRef": _typing.NotRequired[_builtins.object], "trueObj": _typing.NotRequired[_builtins.dict[_builtins.str, _builtins.object]], "trueSchema": _typing.NotRequired[_builtins.object], "tuple": _builtins.list[_builtins.object], "unknownKeyword": _typing.NotRequired[_builtins.object]})

OrdersKitchenSinkInput = _typing.TypedDict("OrdersKitchenSinkInput", {"1starts": _typing.NotRequired[_builtins.str], "Dash-Key": _typing.NotRequired[_builtins.str], "all": _typing.NotRequired[OrdersKitchenSinkInputAll], "allConflict": _typing.NotRequired[_builtins.object], "allNonObject": _typing.NotRequired[_builtins.object], "annotated": _typing.NotRequired[_builtins.str], "arrayRef": _typing.NotRequired[_builtins.str], "closedEmpty": _typing.NotRequired[OrdersKitchenSinkInputClosedEmpty], "constNum": _typing.NotRequired[_builtins.float], "constObj": _typing.NotRequired[_builtins.object], "constString": _typing.NotRequired[_typing.Literal["it's \"quoted\"\n"]], "cycle": _typing.NotRequired[OrdersKitchenSinkInputCycle], "dash-key": _typing.NotRequired[_builtins.str], "emptyAll": _typing.NotRequired[_builtins.dict[_builtins.str, _builtins.object]], "emptyAny": _typing.NotRequired[_typing.Never], "falseSchema": _typing.NotRequired[_typing.Never], "implicitTuple": _typing.NotRequired[_builtins.list[_builtins.object]], "literals": _typing.Literal["a"] | _typing.Literal[1] | _builtins.float | _typing.Literal[-3] | _typing.Literal[True] | None | _builtins.object | _builtins.object | _builtins.float | _builtins.float | _builtins.float, "mapOf": _typing.NotRequired[_builtins.dict[_builtins.str, _builtins.str]], "multi": _typing.NotRequired[_builtins.str | None | _builtins.list[_builtins.int] | _builtins.object], "nested": _typing.NotRequired[_builtins.list[_builtins.list[OrdersKitchenSinkInputNestedItemItem]]], "noType": OrdersKitchenSinkInputNoType, "numericType": _typing.NotRequired[OrdersKitchenSinkInputNumericType], "odd key with spaces": _typing.NotRequired[_builtins.str], "oneOf": _typing.NotRequired[_builtins.str | _builtins.float], "openObj": _typing.NotRequired[_builtins.dict[_builtins.str, _builtins.object]], "openTuple": _typing.NotRequired[_builtins.list[_builtins.object]], "plainArray": _typing.NotRequired[_builtins.list[_builtins.object]], "pointerEscape": _typing.NotRequired[_builtins.int], "rootRef": _typing.NotRequired[OrdersKitchenSinkInputRootRef], "trueObj": _typing.NotRequired[_builtins.dict[_builtins.str, _builtins.object]], "trueSchema": _typing.NotRequired[_builtins.object], "tuple": _builtins.list[_builtins.object], "unknownKeyword": _typing.NotRequired[_builtins.object]})

OrdersWeirdKeysInput = _typing.TypedDict("OrdersWeirdKeysInput", {"": None, "-": _typing.NotRequired[_builtins.bool], "x\"quote": _typing.NotRequired[_builtins.str], "ünïcode": _typing.NotRequired[_builtins.int]})

OrdersWeirdKeysOutput = _typing.TypedDict("OrdersWeirdKeysOutput", {"a.b": _typing.NotRequired[_builtins.str], "nl\nname": _typing.NotRequired[_builtins.str], "tab\tname": _builtins.str})

OrdersMojiInput = _typing.TypedDict("OrdersMojiInput", {"é": _typing.NotRequired[_builtins.str]})

OrdersKitchenSinkInput2 = _typing.TypedDict("OrdersKitchenSinkInput2", {"id": _builtins.str})

class Inputs:
    class orders:
        CallTool: _typing.TypeAlias = _builtins.dict[_builtins.str, _builtins.object]
        call_tool: _typing.TypeAlias = OrdersCallToolInput
        get_order: _typing.TypeAlias = OrdersGetOrderInput2

class Outputs:
    class orders:
        CallTool: _typing.TypeAlias = _builtins.object
        call_tool: _typing.TypeAlias = OrdersCallToolOutput
        get_order: _typing.TypeAlias = _builtins.object

class InputTypes:
    class orders:
        class CallTool:
            pass
        class call_tool:
            pass
        class get_order:
            pass

class OutputTypes:
    class orders:
        class CallTool:
            pass
        class call_tool:
            pass
        class get_order:
            pass

class X123Tools(_typing.Protocol):
    pass

class X123App(_typing.Protocol):
    tools: X123Tools

class PrivateTools(_typing.Protocol):
    pass

class PrivateApp(_typing.Protocol):
    tools: PrivateTools

class ClassApp(_typing.Protocol):
    async def call_tool(self, name: _builtins.str, options: _CallOptions | None = ..., /, **arguments: _builtins.object) -> _ToolResult[_builtins.object]: ...

class ForTools(_typing.Protocol):
    async def x(self, options: _CallOptions | None = ..., /, **arguments: _builtins.object) -> _builtins.object: ...

class ForApp(_typing.Protocol):
    tools: ForTools
    async def call_tool(self, name: _typing.Literal["x"], options: _CallOptions | None = ..., /, **arguments: _builtins.object) -> _ToolResult[_builtins.object]: ...

class OrdersTools(_typing.Protocol):
    async def CallTool(self, options: _CallOptions | None = ..., /, **arguments: _builtins.object) -> _builtins.object: ...
    async def call_tool(self, options: _CallOptions | None = ..., /, **arguments: _typing.Unpack[OrdersCallToolInput]) -> OrdersCallToolOutput: ...
    async def get_order(self, options: _CallOptions | None = ..., /, **arguments: _typing.Unpack[OrdersGetOrderInput2]) -> _builtins.object: ...

class OrdersApp(_typing.Protocol):
    tools: OrdersTools
    @_typing.overload
    async def call_tool(self, name: _typing.Literal["-"], options: _CallOptions | None = ..., /, **arguments: _builtins.object) -> _ToolResult[_builtins.object]: ...
    @_typing.overload
    async def call_tool(self, name: _typing.Literal["123"], options: _CallOptions | None = ..., /, **arguments: _builtins.object) -> _ToolResult[_builtins.object]: ...
    @_typing.overload
    async def call_tool(self, name: _typing.Literal["CallTool"], options: _CallOptions | None = ..., /, **arguments: _builtins.object) -> _ToolResult[_builtins.object]: ...
    @_typing.overload
    async def call_tool(self, name: _typing.Literal["call_tool"], options: _CallOptions | None = ..., /, **arguments: _typing.Unpack[OrdersCallToolInput]) -> _ToolResult[OrdersCallToolOutput]: ...
    @_typing.overload
    async def call_tool(self, name: _typing.Literal["class"], options: _CallOptions | None = ..., /, **arguments: _typing.Unpack[OrdersClassInput]) -> _ToolResult[_builtins.list[_builtins.object]]: ...
    @_typing.overload
    async def call_tool(self, name: _typing.Literal["get-order"], options: _CallOptions | None = ..., /, **arguments: _typing.Unpack[OrdersGetOrderInput]) -> _ToolResult[_builtins.object]: ...
    @_typing.overload
    async def call_tool(self, name: _typing.Literal["get_order"], options: _CallOptions | None = ..., /, **arguments: _typing.Unpack[OrdersGetOrderInput2]) -> _ToolResult[_builtins.object]: ...
    @_typing.overload
    async def call_tool(self, name: _typing.Literal["kitchen-sink"], options: _CallOptions | None = ..., /, **arguments: _typing.Unpack[OrdersKitchenSinkInput]) -> _ToolResult[_builtins.object]: ...
    @_typing.overload
    async def call_tool(self, name: _typing.Literal["weird-keys"], options: _CallOptions | None = ..., /, **arguments: _typing.Unpack[OrdersWeirdKeysInput]) -> _ToolResult[OrdersWeirdKeysOutput]: ...
    @_typing.overload
    async def call_tool(self, name: _typing.Literal["émoji🎉"], options: _CallOptions | None = ..., /, **arguments: _typing.Unpack[OrdersMojiInput]) -> _ToolResult[_builtins.str]: ...

class OrdersTools2(_typing.Protocol):
    pass

class OrdersApp2(_typing.Protocol):
    tools: OrdersTools2
    async def call_tool(self, name: _typing.Literal["kitchen-sink"], options: _CallOptions | None = ..., /, **arguments: _typing.Unpack[OrdersKitchenSinkInput2]) -> _ToolResult[_builtins.bool]: ...

class PayMentsApp(_typing.Protocol):
    async def call_tool(self, name: _builtins.str, options: _CallOptions | None = ..., /, **arguments: _builtins.object) -> _ToolResult[_builtins.object]: ...

class PaymentsApp(_typing.Protocol):
    async def call_tool(self, name: _builtins.str, options: _CallOptions | None = ..., /, **arguments: _builtins.object) -> _ToolResult[_builtins.object]: ...

class UntypedOkApp(_typing.Protocol):
    async def call_tool(self, name: _builtins.str, options: _CallOptions | None = ..., /, **arguments: _builtins.object) -> _ToolResult[_builtins.object]: ...

class WithHyphenTools(_typing.Protocol):
    async def y(self, options: _CallOptions | None = ..., /, **arguments: _builtins.object) -> _builtins.object: ...

class WithHyphenApp(_typing.Protocol):
    tools: WithHyphenTools
    async def call_tool(self, name: _typing.Literal["y"], options: _CallOptions | None = ..., /, **arguments: _builtins.object) -> _ToolResult[_builtins.object]: ...

class NTools(_typing.Protocol):
    async def z(self, options: _CallOptions | None = ..., /, **arguments: _builtins.object) -> _builtins.object: ...

class NApp(_typing.Protocol):
    tools: NTools
    async def call_tool(self, name: _typing.Literal["z"], options: _CallOptions | None = ..., /, **arguments: _builtins.object) -> _ToolResult[_builtins.object]: ...

class _Clients(_typing.Protocol):
    orders: OrdersApp
    payments: PaymentsApp
    untyped_ok: UntypedOkApp
    @_typing.overload
    def __getitem__(self, name: _typing.Literal["123"]) -> X123App: ...
    @_typing.overload
    def __getitem__(self, name: _typing.Literal["_private"]) -> PrivateApp: ...
    @_typing.overload
    def __getitem__(self, name: _typing.Literal["class"]) -> ClassApp: ...
    @_typing.overload
    def __getitem__(self, name: _typing.Literal["for"]) -> ForApp: ...
    @_typing.overload
    def __getitem__(self, name: _typing.Literal["orders"]) -> OrdersApp: ...
    @_typing.overload
    def __getitem__(self, name: _typing.Literal["orders!"]) -> OrdersApp2: ...
    @_typing.overload
    def __getitem__(self, name: _typing.Literal["pay-ments"]) -> PayMentsApp: ...
    @_typing.overload
    def __getitem__(self, name: _typing.Literal["payments"]) -> PaymentsApp: ...
    @_typing.overload
    def __getitem__(self, name: _typing.Literal["untyped_ok"]) -> UntypedOkApp: ...
    @_typing.overload
    def __getitem__(self, name: _typing.Literal["with-hyphen"]) -> WithHyphenApp: ...
    @_typing.overload
    def __getitem__(self, name: _typing.Literal["ünï"]) -> NApp: ...


_identity, _contracts = _json.loads("[{\"name\":\"ch\u00e9ck\\\"out\",\"requires\":{\"123\":{\"name\":\"n\",\"version\":\"1\"},\"_private\":{\"name\":\"p\",\"version\":\"1\"},\"class\":{\"name\":\"c\",\"version\":\"1\"},\"for\":{\"name\":\"keyword\",\"version\":\"1\"},\"orders\":{\"name\":\"orders\",\"version\":\"1.0.0\"},\"orders!\":{\"name\":\"example/orders\",\"version\":\"2\"},\"pay-ments\":{\"name\":\"pay\",\"version\":\"1\"},\"payments\":{\"name\":\"payments\",\"version\":\"2.0.0\"},\"untyped_ok\":{\"name\":\"u\",\"version\":\"1\"},\"with-hyphen\":{\"name\":\"h\",\"version\":\"1\"},\"\u00fcn\u00ef\":{\"name\":\"\u00fc\",\"version\":\"1\"}},\"version\":\"1.0.0-rc.1\"},{\"123\":{\"tools\":[]},\"_private\":{\"tools\":[]},\"for\":{\"tools\":[{\"inputSchema\":{\"type\":\"object\"},\"name\":\"x\"}]},\"orders\":{\"resources\":[{\"outputSchema\":{\"type\":\"string\"},\"uri\":\"res://plain\"},{\"outputSchema\":{\"$defs\":{\"Item\":{\"additionalProperties\":false,\"properties\":{\"id\":{\"type\":\"string\"}},\"required\":[\"id\"],\"type\":\"object\"}},\"additionalProperties\":false,\"properties\":{\"items\":{\"items\":{\"$ref\":\"#/$defs/Item\"},\"type\":\"array\"}},\"required\":[\"items\"],\"type\":\"object\"},\"uri\":\"res://typed\"},{\"outputSchema\":{\"properties\":{\"n\":{\"default\":-0.0,\"maximum\":1e+21,\"minimum\":0.5,\"multipleOf\":1.0,\"type\":\"number\"}},\"type\":\"object\"},\"uri\":\"res://\u00e9/\u00fc\"}],\"tools\":[{\"inputSchema\":{\"type\":\"object\"},\"name\":\"-\"},{\"inputSchema\":{\"type\":\"object\"},\"name\":\"123\"},{\"inputSchema\":{\"type\":\"object\"},\"name\":\"CallTool\"},{\"inputSchema\":{\"additionalProperties\":false,\"properties\":{\"a\":{\"type\":\"integer\"},\"b\":{\"type\":\"string\"}},\"required\":[\"b\"],\"type\":\"object\"},\"name\":\"call_tool\",\"outputSchema\":{\"additionalProperties\":false,\"properties\":{\"x\":{\"type\":\"boolean\"}},\"required\":[\"x\"],\"type\":\"object\"}},{\"inputSchema\":{\"additionalProperties\":false,\"properties\":{\"opt\":{\"additionalProperties\":false,\"properties\":{\"n\":{\"type\":\"number\"}},\"type\":\"object\"}},\"type\":\"object\"},\"name\":\"class\",\"outputSchema\":{\"items\":{\"$ref\":\"#/items\"},\"type\":\"array\"}},{\"inputSchema\":{\"additionalProperties\":false,\"properties\":{\"id\":{\"type\":\"string\"}},\"required\":[\"id\"],\"type\":\"object\"},\"name\":\"get-order\"},{\"inputSchema\":{\"additionalProperties\":false,\"properties\":{\"id\":{\"type\":\"string\"}},\"required\":[\"id\"],\"type\":\"object\"},\"name\":\"get_order\"},{\"inputSchema\":{\"$defs\":{\"9lives\":{\"type\":\"string\"},\"Node\":{\"additionalProperties\":false,\"properties\":{\"next\":{\"$ref\":\"#/$defs/Node\"},\"value\":{\"type\":\"integer\"}},\"required\":[\"value\"],\"type\":\"object\"},\"_private\":{\"type\":\"boolean\"},\"a/b~c\":{\"type\":\"integer\"},\"for\":{\"type\":\"string\"},\"list\":{\"prefixItems\":[{\"type\":\"string\"}],\"type\":\"array\"}},\"additionalProperties\":false,\"definitions\":{\"Legacy\":{\"type\":\"number\"},\"Node\":{\"type\":\"string\"}},\"properties\":{\"1starts\":{\"type\":\"string\"},\"Dash-Key\":{\"type\":\"string\"},\"all\":{\"allOf\":[{\"properties\":{\"a\":{\"type\":\"string\"}},\"required\":[\"a\"],\"type\":\"object\"},{\"properties\":{\"b\":{\"type\":\"integer\"}},\"type\":\"object\"}]},\"allConflict\":{\"allOf\":[{\"properties\":{\"a\":{\"type\":\"string\"}},\"type\":\"object\"},{\"properties\":{\"a\":{\"type\":\"integer\"}},\"type\":\"object\"}]},\"allNonObject\":{\"allOf\":[{\"type\":\"string\"},{\"type\":\"object\"}]},\"annotated\":{\"$comment\":\"c\",\"description\":\"gone\",\"examples\":[1],\"title\":\"t\",\"type\":\"string\"},\"arrayRef\":{\"$ref\":\"#/$defs/list/prefixItems/0\"},\"closedEmpty\":{\"additionalProperties\":false,\"type\":\"object\"},\"constNum\":{\"const\":1e+21},\"constObj\":{\"const\":{\"deep\":[1,2]}},\"constString\":{\"const\":\"it's \\\"quoted\\\"\\n\"},\"cycle\":{\"$ref\":\"#/$defs/Node\"},\"dash-key\":{\"type\":\"string\"},\"emptyAll\":{\"allOf\":[]},\"emptyAny\":{\"anyOf\":[]},\"falseSchema\":false,\"implicitTuple\":{\"minItems\":2.0,\"prefixItems\":[{\"const\":\"x\"}],\"type\":\"array\"},\"literals\":{\"enum\":[\"a\",1,2.5,-3,true,null,[1],{\"k\":\"v\"},1.0,1e+21,-0.0]},\"mapOf\":{\"additionalProperties\":{\"type\":\"string\"},\"type\":\"object\"},\"multi\":{\"items\":{\"type\":\"integer\"},\"type\":[\"string\",\"null\",\"array\",5]},\"nested\":{\"items\":{\"items\":{\"additionalProperties\":false,\"properties\":{\"deep\":{\"type\":\"null\"}},\"type\":\"object\"},\"type\":\"array\"},\"type\":\"array\"},\"noType\":{\"properties\":{\"p\":{\"type\":\"boolean\"}},\"required\":[\"p\",7]},\"numericType\":{\"properties\":{\"p\":{\"type\":\"boolean\"}},\"type\":7},\"odd key with spaces\":{\"type\":\"string\"},\"oneOf\":{\"oneOf\":[{\"type\":\"string\"},{\"type\":\"number\"}]},\"openObj\":{\"additionalProperties\":{\"type\":\"integer\"},\"properties\":{\"x\":{\"type\":\"string\"}},\"type\":\"object\"},\"openTuple\":{\"additionalItems\":{\"type\":\"number\"},\"items\":[{\"type\":\"boolean\"}],\"type\":\"array\"},\"plainArray\":{\"type\":\"array\"},\"pointerEscape\":{\"$ref\":\"#/$defs/a~1b~0c\"},\"rootRef\":{\"$ref\":\"#\"},\"trueObj\":{\"additionalProperties\":true,\"properties\":{\"x\":{\"type\":\"string\"}},\"type\":\"object\"},\"trueSchema\":true,\"tuple\":{\"items\":false,\"minItems\":1,\"prefixItems\":[{\"type\":\"string\"},{\"type\":\"integer\"}],\"type\":\"array\"},\"unknownKeyword\":{\"format\":\"email\"}},\"required\":[\"tuple\",\"literals\",\"noType\"],\"type\":\"object\"},\"name\":\"kitchen-sink\"},{\"inputSchema\":{\"additionalProperties\":false,\"properties\":{\"\":{\"type\":\"null\"},\"-\":{\"type\":\"boolean\"},\"x\\\"quote\":{\"type\":\"string\"},\"\u00fcn\u00efcode\":{\"type\":\"integer\"}},\"required\":[\"\"],\"type\":\"object\"},\"name\":\"weird-keys\",\"outputSchema\":{\"additionalProperties\":false,\"properties\":{\"a.b\":{\"type\":\"string\"},\"nl\\nname\":{\"type\":\"string\"},\"tab\\tname\":{\"type\":\"string\"}},\"required\":[\"tab\\tname\"],\"type\":\"object\"}},{\"inputSchema\":{\"properties\":{\"\u00e9\":{\"type\":\"string\"}},\"type\":\"object\"},\"name\":\"\u00e9moji\U0001f389\",\"outputSchema\":{\"description\":\"\u00e9\",\"type\":\"string\"}}]},\"orders!\":{\"tools\":[{\"inputSchema\":{\"additionalProperties\":false,\"properties\":{\"id\":{\"type\":\"string\"}},\"required\":[\"id\"],\"type\":\"object\"},\"name\":\"kitchen-sink\",\"outputSchema\":{\"type\":\"boolean\"}}]},\"with-hyphen\":{\"tools\":[{\"inputSchema\":{\"type\":\"object\"},\"name\":\"y\"}]},\"\u00fcn\u00ef\":{\"tools\":[{\"inputSchema\":{\"type\":\"object\"},\"name\":\"z\"}]}}]")

apps = _typing.cast(_Clients, _Apps(_identity, _contracts))

def create_apps(bindings: _typing.Mapping[str, _Binding]) -> _Clients:
    return _typing.cast(_Clients, _Apps(_identity, _contracts, bindings))
