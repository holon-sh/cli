# holon-generated-apps-v1 app=checkout
# Input alternatives share a result type; redundant overloads are harmless.
# mypy: disable-error-code=overload-cannot-match
from __future__ import annotations

import json as _json

from holon.client import Apps as _Apps
from holon.client import Binding as _Binding
from holon.client import CallOptions as _CallOptions
from holon.client import CompleteResult as _CompleteResult
from holon.client import ToolResult as _ToolResult

import builtins as _builtins
import typing as _typing

MysqlSelectInputColumnsItem = _typing.TypedDict("MysqlSelectInputColumnsItem", {"column": _builtins.str, "label": _builtins.str})

MysqlSelectInput = _typing.TypedDict("MysqlSelectInput", {"columns": _typing.NotRequired[_builtins.list[_builtins.str] | _builtins.list[MysqlSelectInputColumnsItem] | None], "direction": _typing.NotRequired[_typing.Literal["asc"] | _typing.Literal["desc"]], "table": _builtins.str})

MysqlSelectOutputColumnsItem = _typing.TypedDict("MysqlSelectOutputColumnsItem", {"value": _builtins.float})

MysqlSelectOutput = _typing.TypedDict("MysqlSelectOutput", {"columns": _builtins.list[MysqlSelectOutputColumnsItem]})

MysqlSelectInputsLabeledColumn = _typing.TypedDict("MysqlSelectInputsLabeledColumn", {"column": _builtins.str, "label": _builtins.str})

MysqlSelectOutputsLabeledColumn = _typing.TypedDict("MysqlSelectOutputsLabeledColumn", {"value": _builtins.float})

MysqlSelectOutputsRootColumnsItem = _typing.TypedDict("MysqlSelectOutputsRootColumnsItem", {"value": _builtins.float})

MysqlSelectOutputsRoot = _typing.TypedDict("MysqlSelectOutputsRoot", {"columns": _builtins.list[MysqlSelectOutputsRootColumnsItem]})

OrdersGetOrderInput = _typing.TypedDict("OrdersGetOrderInput", {"count": _typing.NotRequired[_builtins.int], "orderId": _builtins.str})

OrdersGetOrderOutput = _typing.TypedDict("OrdersGetOrderOutput", {"total": _builtins.float})

OrdersThenInput = _typing.TypedDict("OrdersThenInput", {})

SchemasCallToolInput = _typing.TypedDict("SchemasCallToolInput", {"a": _typing.NotRequired[_builtins.int], "b": _builtins.str})

SchemasCallToolOutput = _typing.TypedDict("SchemasCallToolOutput", {"x": _builtins.bool})

SchemasClassInputOpt = _typing.TypedDict("SchemasClassInputOpt", {"n": _typing.NotRequired[_builtins.float]})

SchemasClassInput = _typing.TypedDict("SchemasClassInput", {"opt": _typing.NotRequired[SchemasClassInputOpt]})

SchemasGetOrderInput = _typing.TypedDict("SchemasGetOrderInput", {"id": _builtins.str})

SchemasGetOrderInput2 = _typing.TypedDict("SchemasGetOrderInput2", {"id": _builtins.str})

SchemasKitchenSinkInputAll = _typing.TypedDict("SchemasKitchenSinkInputAll", {"a": _builtins.str, "b": _typing.NotRequired[_builtins.int]})

SchemasKitchenSinkInputClosedEmpty = _typing.TypedDict("SchemasKitchenSinkInputClosedEmpty", {})

SchemasKitchenSinkInputCycle = _typing.TypedDict("SchemasKitchenSinkInputCycle", {"next": _typing.NotRequired[_builtins.object], "value": _builtins.int})

SchemasKitchenSinkInputNestedItemItem = _typing.TypedDict("SchemasKitchenSinkInputNestedItemItem", {"deep": _typing.NotRequired[None]})

SchemasKitchenSinkInputNoType = _typing.TypedDict("SchemasKitchenSinkInputNoType", {"p": _builtins.bool})

SchemasKitchenSinkInputNumericType = _typing.TypedDict("SchemasKitchenSinkInputNumericType", {"p": _typing.NotRequired[_builtins.bool]})

SchemasKitchenSinkInputRootRefAll = _typing.TypedDict("SchemasKitchenSinkInputRootRefAll", {"a": _builtins.str, "b": _typing.NotRequired[_builtins.int]})

SchemasKitchenSinkInputRootRefClosedEmpty = _typing.TypedDict("SchemasKitchenSinkInputRootRefClosedEmpty", {})

SchemasKitchenSinkInputRootRefCycle = _typing.TypedDict("SchemasKitchenSinkInputRootRefCycle", {"next": _typing.NotRequired[_builtins.object], "value": _builtins.int})

SchemasKitchenSinkInputRootRefNestedItemItem = _typing.TypedDict("SchemasKitchenSinkInputRootRefNestedItemItem", {"deep": _typing.NotRequired[None]})

SchemasKitchenSinkInputRootRefNoType = _typing.TypedDict("SchemasKitchenSinkInputRootRefNoType", {"p": _builtins.bool})

SchemasKitchenSinkInputRootRefNumericType = _typing.TypedDict("SchemasKitchenSinkInputRootRefNumericType", {"p": _typing.NotRequired[_builtins.bool]})

SchemasKitchenSinkInputRootRef = _typing.TypedDict("SchemasKitchenSinkInputRootRef", {"1starts": _typing.NotRequired[_builtins.str], "Dash-Key": _typing.NotRequired[_builtins.str], "all": _typing.NotRequired[SchemasKitchenSinkInputRootRefAll], "allConflict": _typing.NotRequired[_builtins.object], "allNonObject": _typing.NotRequired[_builtins.object], "annotated": _typing.NotRequired[_builtins.str], "arrayRef": _typing.NotRequired[_builtins.str], "closedEmpty": _typing.NotRequired[SchemasKitchenSinkInputRootRefClosedEmpty], "constNum": _typing.NotRequired[_builtins.float], "constObj": _typing.NotRequired[_builtins.object], "constString": _typing.NotRequired[_typing.Literal["it's \"quoted\"\n"]], "cycle": _typing.NotRequired[SchemasKitchenSinkInputRootRefCycle], "dash-key": _typing.NotRequired[_builtins.str], "emptyAll": _typing.NotRequired[_builtins.dict[_builtins.str, _builtins.object]], "emptyAny": _typing.NotRequired[_typing.Never], "falseSchema": _typing.NotRequired[_typing.Never], "implicitTuple": _typing.NotRequired[_builtins.list[_builtins.object]], "literals": _typing.Literal["a"] | _typing.Literal[1] | _builtins.float | _typing.Literal[-3] | _typing.Literal[True] | None | _builtins.object | _builtins.object | _builtins.float | _builtins.float | _builtins.float, "mapOf": _typing.NotRequired[_builtins.dict[_builtins.str, _builtins.str]], "multi": _typing.NotRequired[_builtins.str | None | _builtins.list[_builtins.int] | _builtins.object], "nested": _typing.NotRequired[_builtins.list[_builtins.list[SchemasKitchenSinkInputRootRefNestedItemItem]]], "noType": SchemasKitchenSinkInputRootRefNoType, "numericType": _typing.NotRequired[SchemasKitchenSinkInputRootRefNumericType], "odd key with spaces": _typing.NotRequired[_builtins.str], "oneOf": _typing.NotRequired[_builtins.str | _builtins.float], "openObj": _typing.NotRequired[_builtins.dict[_builtins.str, _builtins.object]], "openTuple": _typing.NotRequired[_builtins.list[_builtins.object]], "plainArray": _typing.NotRequired[_builtins.list[_builtins.object]], "pointerEscape": _typing.NotRequired[_builtins.int], "rootRef": _typing.NotRequired[_builtins.object], "trueObj": _typing.NotRequired[_builtins.dict[_builtins.str, _builtins.object]], "trueSchema": _typing.NotRequired[_builtins.object], "tuple": _builtins.list[_builtins.object], "unknownKeyword": _typing.NotRequired[_builtins.object]})

SchemasKitchenSinkInput = _typing.TypedDict("SchemasKitchenSinkInput", {"1starts": _typing.NotRequired[_builtins.str], "Dash-Key": _typing.NotRequired[_builtins.str], "all": _typing.NotRequired[SchemasKitchenSinkInputAll], "allConflict": _typing.NotRequired[_builtins.object], "allNonObject": _typing.NotRequired[_builtins.object], "annotated": _typing.NotRequired[_builtins.str], "arrayRef": _typing.NotRequired[_builtins.str], "closedEmpty": _typing.NotRequired[SchemasKitchenSinkInputClosedEmpty], "constNum": _typing.NotRequired[_builtins.float], "constObj": _typing.NotRequired[_builtins.object], "constString": _typing.NotRequired[_typing.Literal["it's \"quoted\"\n"]], "cycle": _typing.NotRequired[SchemasKitchenSinkInputCycle], "dash-key": _typing.NotRequired[_builtins.str], "emptyAll": _typing.NotRequired[_builtins.dict[_builtins.str, _builtins.object]], "emptyAny": _typing.NotRequired[_typing.Never], "falseSchema": _typing.NotRequired[_typing.Never], "implicitTuple": _typing.NotRequired[_builtins.list[_builtins.object]], "literals": _typing.Literal["a"] | _typing.Literal[1] | _builtins.float | _typing.Literal[-3] | _typing.Literal[True] | None | _builtins.object | _builtins.object | _builtins.float | _builtins.float | _builtins.float, "mapOf": _typing.NotRequired[_builtins.dict[_builtins.str, _builtins.str]], "multi": _typing.NotRequired[_builtins.str | None | _builtins.list[_builtins.int] | _builtins.object], "nested": _typing.NotRequired[_builtins.list[_builtins.list[SchemasKitchenSinkInputNestedItemItem]]], "noType": SchemasKitchenSinkInputNoType, "numericType": _typing.NotRequired[SchemasKitchenSinkInputNumericType], "odd key with spaces": _typing.NotRequired[_builtins.str], "oneOf": _typing.NotRequired[_builtins.str | _builtins.float], "openObj": _typing.NotRequired[_builtins.dict[_builtins.str, _builtins.object]], "openTuple": _typing.NotRequired[_builtins.list[_builtins.object]], "plainArray": _typing.NotRequired[_builtins.list[_builtins.object]], "pointerEscape": _typing.NotRequired[_builtins.int], "rootRef": _typing.NotRequired[SchemasKitchenSinkInputRootRef], "trueObj": _typing.NotRequired[_builtins.dict[_builtins.str, _builtins.object]], "trueSchema": _typing.NotRequired[_builtins.object], "tuple": _builtins.list[_builtins.object], "unknownKeyword": _typing.NotRequired[_builtins.object]})

SchemasWeirdKeysInput = _typing.TypedDict("SchemasWeirdKeysInput", {"": None, "-": _typing.NotRequired[_builtins.bool], "x\"quote": _typing.NotRequired[_builtins.str], "ünïcode": _typing.NotRequired[_builtins.int]})

SchemasWeirdKeysOutput = _typing.TypedDict("SchemasWeirdKeysOutput", {"a.b": _typing.NotRequired[_builtins.str], "nl\nname": _typing.NotRequired[_builtins.str], "tab\tname": _builtins.str})

SchemasMojiInput = _typing.TypedDict("SchemasMojiInput", {"é": _typing.NotRequired[_builtins.str]})

WithHyphenGetOrderInput = _typing.TypedDict("WithHyphenGetOrderInput", {"count": _typing.NotRequired[_builtins.int], "orderId": _builtins.str})

WithHyphenGetOrderOutput = _typing.TypedDict("WithHyphenGetOrderOutput", {"total": _builtins.float})

WithHyphenThenInput = _typing.TypedDict("WithHyphenThenInput", {})

class Inputs:
    class empty:
        pass
    class mysql:
        select: _typing.TypeAlias = MysqlSelectInput
    class orders:
        get_order: _typing.TypeAlias = OrdersGetOrderInput
        then: _typing.TypeAlias = OrdersThenInput
    class schemas:
        CallTool: _typing.TypeAlias = _builtins.dict[_builtins.str, _builtins.object]
        call_tool: _typing.TypeAlias = SchemasCallToolInput
        get_order: _typing.TypeAlias = SchemasGetOrderInput2

class Outputs:
    class empty:
        pass
    class mysql:
        select: _typing.TypeAlias = MysqlSelectOutput
    class orders:
        get_order: _typing.TypeAlias = OrdersGetOrderOutput
    class schemas:
        call_tool: _typing.TypeAlias = SchemasCallToolOutput

class InputTypes:
    class empty:
        pass
    class mysql:
        class select:
            Direction: _typing.TypeAlias = _typing.Literal["asc"] | _typing.Literal["desc"]
            LabeledColumn: _typing.TypeAlias = MysqlSelectInputsLabeledColumn
    class orders:
        class get_order:
            pass
        class then:
            pass
    class schemas:
        class CallTool:
            pass
        class call_tool:
            pass
        class get_order:
            pass

class OutputTypes:
    class empty:
        pass
    class mysql:
        class select:
            LabeledColumn: _typing.TypeAlias = MysqlSelectOutputsLabeledColumn
            Root: _typing.TypeAlias = MysqlSelectOutputsRoot
    class orders:
        class get_order:
            pass
    class schemas:
        class call_tool:
            pass

class EmptyTools(_typing.Protocol):
    pass

class EmptyApp(_typing.Protocol):
    tools: EmptyTools

class MysqlTools(_typing.Protocol):
    async def select(self, options: _CallOptions | None = ..., /, **arguments: _typing.Unpack[MysqlSelectInput]) -> MysqlSelectOutput: ...

class MysqlApp(_typing.Protocol):
    tools: MysqlTools
    async def call_tool(self, name: _typing.Literal["select"], options: _CallOptions | None = ..., /, **arguments: _typing.Unpack[MysqlSelectInput]) -> _ToolResult[MysqlSelectOutput]: ...

class OrdersTools(_typing.Protocol):
    async def get_order(self, options: _CallOptions | None = ..., /, **arguments: _typing.Unpack[OrdersGetOrderInput]) -> OrdersGetOrderOutput: ...
    async def then(self, options: _CallOptions | None = ..., /, **arguments: _typing.Unpack[OrdersThenInput]) -> _CompleteResult[_builtins.object]: ...

class OrdersApp(_typing.Protocol):
    tools: OrdersTools
    @_typing.overload
    async def call_tool(self, name: _typing.Literal["get_order"], options: _CallOptions | None = ..., /, **arguments: _typing.Unpack[OrdersGetOrderInput]) -> _ToolResult[OrdersGetOrderOutput]: ...
    @_typing.overload
    async def call_tool(self, name: _typing.Literal["then"], options: _CallOptions | None = ..., /, **arguments: _typing.Unpack[OrdersThenInput]) -> _ToolResult[_builtins.object]: ...

class SchemasTools(_typing.Protocol):
    async def CallTool(self, options: _CallOptions | None = ..., /, **arguments: _builtins.object) -> _CompleteResult[_builtins.object]: ...
    async def call_tool(self, options: _CallOptions | None = ..., /, **arguments: _typing.Unpack[SchemasCallToolInput]) -> SchemasCallToolOutput: ...
    async def get_order(self, options: _CallOptions | None = ..., /, **arguments: _typing.Unpack[SchemasGetOrderInput2]) -> _CompleteResult[_builtins.object]: ...

class SchemasApp(_typing.Protocol):
    tools: SchemasTools
    @_typing.overload
    async def call_tool(self, name: _typing.Literal["-"], options: _CallOptions | None = ..., /, **arguments: _builtins.object) -> _ToolResult[_builtins.object]: ...
    @_typing.overload
    async def call_tool(self, name: _typing.Literal["123"], options: _CallOptions | None = ..., /, **arguments: _builtins.object) -> _ToolResult[_builtins.object]: ...
    @_typing.overload
    async def call_tool(self, name: _typing.Literal["CallTool"], options: _CallOptions | None = ..., /, **arguments: _builtins.object) -> _ToolResult[_builtins.object]: ...
    @_typing.overload
    async def call_tool(self, name: _typing.Literal["call_tool"], options: _CallOptions | None = ..., /, **arguments: _typing.Unpack[SchemasCallToolInput]) -> _ToolResult[SchemasCallToolOutput]: ...
    @_typing.overload
    async def call_tool(self, name: _typing.Literal["class"], options: _CallOptions | None = ..., /, **arguments: _typing.Unpack[SchemasClassInput]) -> _ToolResult[_builtins.list[_builtins.object]]: ...
    @_typing.overload
    async def call_tool(self, name: _typing.Literal["get-order"], options: _CallOptions | None = ..., /, **arguments: _typing.Unpack[SchemasGetOrderInput]) -> _ToolResult[_builtins.object]: ...
    @_typing.overload
    async def call_tool(self, name: _typing.Literal["get_order"], options: _CallOptions | None = ..., /, **arguments: _typing.Unpack[SchemasGetOrderInput2]) -> _ToolResult[_builtins.object]: ...
    @_typing.overload
    async def call_tool(self, name: _typing.Literal["kitchen-sink"], options: _CallOptions | None = ..., /, **arguments: _typing.Unpack[SchemasKitchenSinkInput]) -> _ToolResult[_builtins.object]: ...
    @_typing.overload
    async def call_tool(self, name: _typing.Literal["weird-keys"], options: _CallOptions | None = ..., /, **arguments: _typing.Unpack[SchemasWeirdKeysInput]) -> _ToolResult[SchemasWeirdKeysOutput]: ...
    @_typing.overload
    async def call_tool(self, name: _typing.Literal["émoji🎉"], options: _CallOptions | None = ..., /, **arguments: _typing.Unpack[SchemasMojiInput]) -> _ToolResult[_builtins.str]: ...

class WithHyphenTools(_typing.Protocol):
    async def get_order(self, options: _CallOptions | None = ..., /, **arguments: _typing.Unpack[WithHyphenGetOrderInput]) -> WithHyphenGetOrderOutput: ...
    async def then(self, options: _CallOptions | None = ..., /, **arguments: _typing.Unpack[WithHyphenThenInput]) -> _CompleteResult[_builtins.object]: ...

class WithHyphenApp(_typing.Protocol):
    tools: WithHyphenTools
    @_typing.overload
    async def call_tool(self, name: _typing.Literal["get_order"], options: _CallOptions | None = ..., /, **arguments: _typing.Unpack[WithHyphenGetOrderInput]) -> _ToolResult[WithHyphenGetOrderOutput]: ...
    @_typing.overload
    async def call_tool(self, name: _typing.Literal["then"], options: _CallOptions | None = ..., /, **arguments: _typing.Unpack[WithHyphenThenInput]) -> _ToolResult[_builtins.object]: ...

class _Clients(_typing.Protocol):
    empty: EmptyApp
    mysql: MysqlApp
    orders: OrdersApp
    schemas: SchemasApp
    @_typing.overload
    def __getitem__(self, name: _typing.Literal["empty"]) -> EmptyApp: ...
    @_typing.overload
    def __getitem__(self, name: _typing.Literal["mysql"]) -> MysqlApp: ...
    @_typing.overload
    def __getitem__(self, name: _typing.Literal["orders"]) -> OrdersApp: ...
    @_typing.overload
    def __getitem__(self, name: _typing.Literal["schemas"]) -> SchemasApp: ...
    @_typing.overload
    def __getitem__(self, name: _typing.Literal["with-hyphen"]) -> WithHyphenApp: ...


_identity, _contracts = _json.loads("[{\"name\":\"checkout\",\"requires\":{\"orders\":{\"name\":\"orders\",\"version\":\"1.0.0\"}},\"version\":\"1.0.0\"},{\"empty\":{\"tools\":[]},\"mysql\":{\"tools\":[{\"inputSchema\":{\"$defs\":{\"Direction\":{\"enum\":[\"asc\",\"desc\"]},\"LabeledColumn\":{\"additionalProperties\":false,\"properties\":{\"column\":{\"type\":\"string\"},\"label\":{\"type\":\"string\"}},\"required\":[\"column\",\"label\"],\"type\":\"object\"}},\"additionalProperties\":false,\"properties\":{\"columns\":{\"anyOf\":[{\"items\":{\"type\":\"string\"},\"type\":\"array\"},{\"items\":{\"$ref\":\"#/$defs/LabeledColumn\"},\"type\":\"array\"},{\"type\":\"null\"}]},\"direction\":{\"$ref\":\"#/$defs/Direction\"},\"table\":{\"type\":\"string\"}},\"required\":[\"table\"],\"type\":\"object\"},\"name\":\"select\",\"outputSchema\":{\"$ref\":\"#/definitions/Root\",\"definitions\":{\"LabeledColumn\":{\"additionalProperties\":false,\"properties\":{\"value\":{\"type\":\"number\"}},\"required\":[\"value\"],\"type\":\"object\"},\"Root\":{\"additionalProperties\":false,\"properties\":{\"columns\":{\"items\":{\"$ref\":\"#/definitions/LabeledColumn\"},\"type\":\"array\"}},\"required\":[\"columns\"],\"type\":\"object\"}}}}]},\"orders\":{\"resources\":[{\"outputSchema\":{\"properties\":{\"total\":{\"type\":\"number\"}},\"required\":[\"total\"],\"type\":\"object\"},\"uri\":\"orders://summary\"}],\"tools\":[{\"inputSchema\":{\"additionalProperties\":false,\"properties\":{\"count\":{\"type\":\"integer\"},\"orderId\":{\"type\":\"string\"}},\"required\":[\"orderId\"],\"type\":\"object\"},\"name\":\"get_order\",\"outputSchema\":{\"properties\":{\"total\":{\"type\":\"number\"}},\"required\":[\"total\"],\"type\":\"object\"}},{\"inputSchema\":{\"additionalProperties\":false,\"type\":\"object\"},\"name\":\"then\"}]},\"schemas\":{\"resources\":[{\"outputSchema\":{\"type\":\"string\"},\"uri\":\"res://plain\"},{\"outputSchema\":{\"$defs\":{\"Item\":{\"additionalProperties\":false,\"properties\":{\"id\":{\"type\":\"string\"}},\"required\":[\"id\"],\"type\":\"object\"}},\"additionalProperties\":false,\"properties\":{\"items\":{\"items\":{\"$ref\":\"#/$defs/Item\"},\"type\":\"array\"}},\"required\":[\"items\"],\"type\":\"object\"},\"uri\":\"res://typed\"},{\"outputSchema\":{\"properties\":{\"n\":{\"default\":-0.0,\"maximum\":1e+21,\"minimum\":0.5,\"multipleOf\":1.0,\"type\":\"number\"}},\"type\":\"object\"},\"uri\":\"res://\u00e9/\u00fc\"}],\"tools\":[{\"inputSchema\":{\"type\":\"object\"},\"name\":\"-\"},{\"inputSchema\":{\"type\":\"object\"},\"name\":\"123\"},{\"inputSchema\":{\"type\":\"object\"},\"name\":\"CallTool\"},{\"inputSchema\":{\"additionalProperties\":false,\"properties\":{\"a\":{\"type\":\"integer\"},\"b\":{\"type\":\"string\"}},\"required\":[\"b\"],\"type\":\"object\"},\"name\":\"call_tool\",\"outputSchema\":{\"additionalProperties\":false,\"properties\":{\"x\":{\"type\":\"boolean\"}},\"required\":[\"x\"],\"type\":\"object\"}},{\"inputSchema\":{\"additionalProperties\":false,\"properties\":{\"opt\":{\"additionalProperties\":false,\"properties\":{\"n\":{\"type\":\"number\"}},\"type\":\"object\"}},\"type\":\"object\"},\"name\":\"class\",\"outputSchema\":{\"items\":{\"$ref\":\"#/items\"},\"type\":\"array\"}},{\"inputSchema\":{\"additionalProperties\":false,\"properties\":{\"id\":{\"type\":\"string\"}},\"required\":[\"id\"],\"type\":\"object\"},\"name\":\"get-order\"},{\"inputSchema\":{\"additionalProperties\":false,\"properties\":{\"id\":{\"type\":\"string\"}},\"required\":[\"id\"],\"type\":\"object\"},\"name\":\"get_order\"},{\"inputSchema\":{\"$defs\":{\"9lives\":{\"type\":\"string\"},\"Node\":{\"additionalProperties\":false,\"properties\":{\"next\":{\"$ref\":\"#/$defs/Node\"},\"value\":{\"type\":\"integer\"}},\"required\":[\"value\"],\"type\":\"object\"},\"_private\":{\"type\":\"boolean\"},\"a/b~c\":{\"type\":\"integer\"},\"for\":{\"type\":\"string\"},\"list\":{\"prefixItems\":[{\"type\":\"string\"}],\"type\":\"array\"}},\"additionalProperties\":false,\"definitions\":{\"Legacy\":{\"type\":\"number\"},\"Node\":{\"type\":\"string\"}},\"properties\":{\"1starts\":{\"type\":\"string\"},\"Dash-Key\":{\"type\":\"string\"},\"all\":{\"allOf\":[{\"properties\":{\"a\":{\"type\":\"string\"}},\"required\":[\"a\"],\"type\":\"object\"},{\"properties\":{\"b\":{\"type\":\"integer\"}},\"type\":\"object\"}]},\"allConflict\":{\"allOf\":[{\"properties\":{\"a\":{\"type\":\"string\"}},\"type\":\"object\"},{\"properties\":{\"a\":{\"type\":\"integer\"}},\"type\":\"object\"}]},\"allNonObject\":{\"allOf\":[{\"type\":\"string\"},{\"type\":\"object\"}]},\"annotated\":{\"$comment\":\"c\",\"description\":\"gone\",\"examples\":[1],\"title\":\"t\",\"type\":\"string\"},\"arrayRef\":{\"$ref\":\"#/$defs/list/prefixItems/0\"},\"closedEmpty\":{\"additionalProperties\":false,\"type\":\"object\"},\"constNum\":{\"const\":1e+21},\"constObj\":{\"const\":{\"deep\":[1,2]}},\"constString\":{\"const\":\"it's \\\"quoted\\\"\\n\"},\"cycle\":{\"$ref\":\"#/$defs/Node\"},\"dash-key\":{\"type\":\"string\"},\"emptyAll\":{\"allOf\":[]},\"emptyAny\":{\"anyOf\":[]},\"falseSchema\":false,\"implicitTuple\":{\"minItems\":2.0,\"prefixItems\":[{\"const\":\"x\"}],\"type\":\"array\"},\"literals\":{\"enum\":[\"a\",1,2.5,-3,true,null,[1],{\"k\":\"v\"},1.0,1e+21,-0.0]},\"mapOf\":{\"additionalProperties\":{\"type\":\"string\"},\"type\":\"object\"},\"multi\":{\"items\":{\"type\":\"integer\"},\"type\":[\"string\",\"null\",\"array\",5]},\"nested\":{\"items\":{\"items\":{\"additionalProperties\":false,\"properties\":{\"deep\":{\"type\":\"null\"}},\"type\":\"object\"},\"type\":\"array\"},\"type\":\"array\"},\"noType\":{\"properties\":{\"p\":{\"type\":\"boolean\"}},\"required\":[\"p\",7]},\"numericType\":{\"properties\":{\"p\":{\"type\":\"boolean\"}},\"type\":7},\"odd key with spaces\":{\"type\":\"string\"},\"oneOf\":{\"oneOf\":[{\"type\":\"string\"},{\"type\":\"number\"}]},\"openObj\":{\"additionalProperties\":{\"type\":\"integer\"},\"properties\":{\"x\":{\"type\":\"string\"}},\"type\":\"object\"},\"openTuple\":{\"additionalItems\":{\"type\":\"number\"},\"items\":[{\"type\":\"boolean\"}],\"type\":\"array\"},\"plainArray\":{\"type\":\"array\"},\"pointerEscape\":{\"$ref\":\"#/$defs/a~1b~0c\"},\"rootRef\":{\"$ref\":\"#\"},\"trueObj\":{\"additionalProperties\":true,\"properties\":{\"x\":{\"type\":\"string\"}},\"type\":\"object\"},\"trueSchema\":true,\"tuple\":{\"items\":false,\"minItems\":1,\"prefixItems\":[{\"type\":\"string\"},{\"type\":\"integer\"}],\"type\":\"array\"},\"unknownKeyword\":{\"format\":\"email\"}},\"required\":[\"tuple\",\"literals\",\"noType\"],\"type\":\"object\"},\"name\":\"kitchen-sink\"},{\"inputSchema\":{\"additionalProperties\":false,\"properties\":{\"\":{\"type\":\"null\"},\"-\":{\"type\":\"boolean\"},\"x\\\"quote\":{\"type\":\"string\"},\"\u00fcn\u00efcode\":{\"type\":\"integer\"}},\"required\":[\"\"],\"type\":\"object\"},\"name\":\"weird-keys\",\"outputSchema\":{\"additionalProperties\":false,\"properties\":{\"a.b\":{\"type\":\"string\"},\"nl\\nname\":{\"type\":\"string\"},\"tab\\tname\":{\"type\":\"string\"}},\"required\":[\"tab\\tname\"],\"type\":\"object\"}},{\"inputSchema\":{\"properties\":{\"\u00e9\":{\"type\":\"string\"}},\"type\":\"object\"},\"name\":\"\u00e9moji\U0001f389\",\"outputSchema\":{\"description\":\"\u00e9\",\"type\":\"string\"}}]},\"with-hyphen\":{\"resources\":[{\"outputSchema\":{\"properties\":{\"total\":{\"type\":\"number\"}},\"required\":[\"total\"],\"type\":\"object\"},\"uri\":\"orders://summary\"}],\"tools\":[{\"inputSchema\":{\"additionalProperties\":false,\"properties\":{\"count\":{\"type\":\"integer\"},\"orderId\":{\"type\":\"string\"}},\"required\":[\"orderId\"],\"type\":\"object\"},\"name\":\"get_order\",\"outputSchema\":{\"properties\":{\"total\":{\"type\":\"number\"}},\"required\":[\"total\"],\"type\":\"object\"}},{\"inputSchema\":{\"additionalProperties\":false,\"type\":\"object\"},\"name\":\"then\"}]}}]")

apps = _typing.cast(_Clients, _Apps(_identity, _contracts))

def create_apps(bindings: _typing.Mapping[str, _Binding]) -> _Clients:
    return _typing.cast(_Clients, _Apps(_identity, _contracts, bindings))
