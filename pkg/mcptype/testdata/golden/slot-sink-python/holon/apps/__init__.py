# holon-generated-apps-v1 app=checkout
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

OrdersCallToolInput2 = _typing.TypedDict("OrdersCallToolInput2", {"a": _typing.NotRequired[_builtins.int], "b": _builtins.str})

OrdersCallToolOutput2 = _typing.TypedDict("OrdersCallToolOutput2", {"x": _builtins.bool})

OrdersClassInputOpt2 = _typing.TypedDict("OrdersClassInputOpt2", {"n": _typing.NotRequired[_builtins.float]})

OrdersClassInput2 = _typing.TypedDict("OrdersClassInput2", {"opt": _typing.NotRequired[OrdersClassInputOpt2]})

OrdersGetOrderInput3 = _typing.TypedDict("OrdersGetOrderInput3", {"id": _builtins.str})

OrdersGetOrderInput4 = _typing.TypedDict("OrdersGetOrderInput4", {"id": _builtins.str})

OrdersKitchenSinkInputAll2 = _typing.TypedDict("OrdersKitchenSinkInputAll2", {"a": _builtins.str, "b": _typing.NotRequired[_builtins.int]})

OrdersKitchenSinkInputClosedEmpty2 = _typing.TypedDict("OrdersKitchenSinkInputClosedEmpty2", {})

OrdersKitchenSinkInputCycle2 = _typing.TypedDict("OrdersKitchenSinkInputCycle2", {"next": _typing.NotRequired[_builtins.object], "value": _builtins.int})

OrdersKitchenSinkInputNestedItemItem2 = _typing.TypedDict("OrdersKitchenSinkInputNestedItemItem2", {"deep": _typing.NotRequired[None]})

OrdersKitchenSinkInputNoType2 = _typing.TypedDict("OrdersKitchenSinkInputNoType2", {"p": _builtins.bool})

OrdersKitchenSinkInputNumericType2 = _typing.TypedDict("OrdersKitchenSinkInputNumericType2", {"p": _typing.NotRequired[_builtins.bool]})

OrdersKitchenSinkInputRootRefAll2 = _typing.TypedDict("OrdersKitchenSinkInputRootRefAll2", {"a": _builtins.str, "b": _typing.NotRequired[_builtins.int]})

OrdersKitchenSinkInputRootRefClosedEmpty2 = _typing.TypedDict("OrdersKitchenSinkInputRootRefClosedEmpty2", {})

OrdersKitchenSinkInputRootRefCycle2 = _typing.TypedDict("OrdersKitchenSinkInputRootRefCycle2", {"next": _typing.NotRequired[_builtins.object], "value": _builtins.int})

OrdersKitchenSinkInputRootRefNestedItemItem2 = _typing.TypedDict("OrdersKitchenSinkInputRootRefNestedItemItem2", {"deep": _typing.NotRequired[None]})

OrdersKitchenSinkInputRootRefNoType2 = _typing.TypedDict("OrdersKitchenSinkInputRootRefNoType2", {"p": _builtins.bool})

OrdersKitchenSinkInputRootRefNumericType2 = _typing.TypedDict("OrdersKitchenSinkInputRootRefNumericType2", {"p": _typing.NotRequired[_builtins.bool]})

OrdersKitchenSinkInputRootRef2 = _typing.TypedDict("OrdersKitchenSinkInputRootRef2", {"1starts": _typing.NotRequired[_builtins.str], "Dash-Key": _typing.NotRequired[_builtins.str], "all": _typing.NotRequired[OrdersKitchenSinkInputRootRefAll2], "allConflict": _typing.NotRequired[_builtins.object], "allNonObject": _typing.NotRequired[_builtins.object], "annotated": _typing.NotRequired[_builtins.str], "arrayRef": _typing.NotRequired[_builtins.str], "closedEmpty": _typing.NotRequired[OrdersKitchenSinkInputRootRefClosedEmpty2], "constNum": _typing.NotRequired[_builtins.float], "constObj": _typing.NotRequired[_builtins.object], "constString": _typing.NotRequired[_typing.Literal["it's \"quoted\"\n"]], "cycle": _typing.NotRequired[OrdersKitchenSinkInputRootRefCycle2], "dash-key": _typing.NotRequired[_builtins.str], "emptyAll": _typing.NotRequired[_builtins.dict[_builtins.str, _builtins.object]], "emptyAny": _typing.NotRequired[_typing.Never], "falseSchema": _typing.NotRequired[_typing.Never], "implicitTuple": _typing.NotRequired[_builtins.list[_builtins.object]], "literals": _typing.Literal["a"] | _typing.Literal[1] | _builtins.float | _typing.Literal[-3] | _typing.Literal[True] | None | _builtins.object | _builtins.object | _builtins.float | _builtins.float | _builtins.float, "mapOf": _typing.NotRequired[_builtins.dict[_builtins.str, _builtins.str]], "multi": _typing.NotRequired[_builtins.str | None | _builtins.list[_builtins.int] | _builtins.object], "nested": _typing.NotRequired[_builtins.list[_builtins.list[OrdersKitchenSinkInputRootRefNestedItemItem2]]], "noType": OrdersKitchenSinkInputRootRefNoType2, "numericType": _typing.NotRequired[OrdersKitchenSinkInputRootRefNumericType2], "odd key with spaces": _typing.NotRequired[_builtins.str], "oneOf": _typing.NotRequired[_builtins.str | _builtins.float], "openObj": _typing.NotRequired[_builtins.dict[_builtins.str, _builtins.object]], "openTuple": _typing.NotRequired[_builtins.list[_builtins.object]], "plainArray": _typing.NotRequired[_builtins.list[_builtins.object]], "pointerEscape": _typing.NotRequired[_builtins.int], "rootRef": _typing.NotRequired[_builtins.object], "trueObj": _typing.NotRequired[_builtins.dict[_builtins.str, _builtins.object]], "trueSchema": _typing.NotRequired[_builtins.object], "tuple": _builtins.list[_builtins.object], "unknownKeyword": _typing.NotRequired[_builtins.object]})

OrdersKitchenSinkInput2 = _typing.TypedDict("OrdersKitchenSinkInput2", {"1starts": _typing.NotRequired[_builtins.str], "Dash-Key": _typing.NotRequired[_builtins.str], "all": _typing.NotRequired[OrdersKitchenSinkInputAll2], "allConflict": _typing.NotRequired[_builtins.object], "allNonObject": _typing.NotRequired[_builtins.object], "annotated": _typing.NotRequired[_builtins.str], "arrayRef": _typing.NotRequired[_builtins.str], "closedEmpty": _typing.NotRequired[OrdersKitchenSinkInputClosedEmpty2], "constNum": _typing.NotRequired[_builtins.float], "constObj": _typing.NotRequired[_builtins.object], "constString": _typing.NotRequired[_typing.Literal["it's \"quoted\"\n"]], "cycle": _typing.NotRequired[OrdersKitchenSinkInputCycle2], "dash-key": _typing.NotRequired[_builtins.str], "emptyAll": _typing.NotRequired[_builtins.dict[_builtins.str, _builtins.object]], "emptyAny": _typing.NotRequired[_typing.Never], "falseSchema": _typing.NotRequired[_typing.Never], "implicitTuple": _typing.NotRequired[_builtins.list[_builtins.object]], "literals": _typing.Literal["a"] | _typing.Literal[1] | _builtins.float | _typing.Literal[-3] | _typing.Literal[True] | None | _builtins.object | _builtins.object | _builtins.float | _builtins.float | _builtins.float, "mapOf": _typing.NotRequired[_builtins.dict[_builtins.str, _builtins.str]], "multi": _typing.NotRequired[_builtins.str | None | _builtins.list[_builtins.int] | _builtins.object], "nested": _typing.NotRequired[_builtins.list[_builtins.list[OrdersKitchenSinkInputNestedItemItem2]]], "noType": OrdersKitchenSinkInputNoType2, "numericType": _typing.NotRequired[OrdersKitchenSinkInputNumericType2], "odd key with spaces": _typing.NotRequired[_builtins.str], "oneOf": _typing.NotRequired[_builtins.str | _builtins.float], "openObj": _typing.NotRequired[_builtins.dict[_builtins.str, _builtins.object]], "openTuple": _typing.NotRequired[_builtins.list[_builtins.object]], "plainArray": _typing.NotRequired[_builtins.list[_builtins.object]], "pointerEscape": _typing.NotRequired[_builtins.int], "rootRef": _typing.NotRequired[OrdersKitchenSinkInputRootRef2], "trueObj": _typing.NotRequired[_builtins.dict[_builtins.str, _builtins.object]], "trueSchema": _typing.NotRequired[_builtins.object], "tuple": _builtins.list[_builtins.object], "unknownKeyword": _typing.NotRequired[_builtins.object]})

OrdersWeirdKeysInput2 = _typing.TypedDict("OrdersWeirdKeysInput2", {"": None, "-": _typing.NotRequired[_builtins.bool], "x\"quote": _typing.NotRequired[_builtins.str], "ünïcode": _typing.NotRequired[_builtins.int]})

OrdersWeirdKeysOutput2 = _typing.TypedDict("OrdersWeirdKeysOutput2", {"a.b": _typing.NotRequired[_builtins.str], "nl\nname": _typing.NotRequired[_builtins.str], "tab\tname": _builtins.str})

OrdersMojiInput2 = _typing.TypedDict("OrdersMojiInput2", {"é": _typing.NotRequired[_builtins.str]})

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
    async def CallTool(self, options: _CallOptions | None = ..., /, **arguments: _builtins.object) -> _builtins.object: ...
    async def call_tool(self, options: _CallOptions | None = ..., /, **arguments: _typing.Unpack[OrdersCallToolInput2]) -> OrdersCallToolOutput2: ...
    async def get_order(self, options: _CallOptions | None = ..., /, **arguments: _typing.Unpack[OrdersGetOrderInput4]) -> _builtins.object: ...

class OrdersApp2(_typing.Protocol):
    tools: OrdersTools2
    @_typing.overload
    async def call_tool(self, name: _typing.Literal["-"], options: _CallOptions | None = ..., /, **arguments: _builtins.object) -> _ToolResult[_builtins.object]: ...
    @_typing.overload
    async def call_tool(self, name: _typing.Literal["123"], options: _CallOptions | None = ..., /, **arguments: _builtins.object) -> _ToolResult[_builtins.object]: ...
    @_typing.overload
    async def call_tool(self, name: _typing.Literal["CallTool"], options: _CallOptions | None = ..., /, **arguments: _builtins.object) -> _ToolResult[_builtins.object]: ...
    @_typing.overload
    async def call_tool(self, name: _typing.Literal["call_tool"], options: _CallOptions | None = ..., /, **arguments: _typing.Unpack[OrdersCallToolInput2]) -> _ToolResult[OrdersCallToolOutput2]: ...
    @_typing.overload
    async def call_tool(self, name: _typing.Literal["class"], options: _CallOptions | None = ..., /, **arguments: _typing.Unpack[OrdersClassInput2]) -> _ToolResult[_builtins.list[_builtins.object]]: ...
    @_typing.overload
    async def call_tool(self, name: _typing.Literal["get-order"], options: _CallOptions | None = ..., /, **arguments: _typing.Unpack[OrdersGetOrderInput3]) -> _ToolResult[_builtins.object]: ...
    @_typing.overload
    async def call_tool(self, name: _typing.Literal["get_order"], options: _CallOptions | None = ..., /, **arguments: _typing.Unpack[OrdersGetOrderInput4]) -> _ToolResult[_builtins.object]: ...
    @_typing.overload
    async def call_tool(self, name: _typing.Literal["kitchen-sink"], options: _CallOptions | None = ..., /, **arguments: _typing.Unpack[OrdersKitchenSinkInput2]) -> _ToolResult[_builtins.object]: ...
    @_typing.overload
    async def call_tool(self, name: _typing.Literal["weird-keys"], options: _CallOptions | None = ..., /, **arguments: _typing.Unpack[OrdersWeirdKeysInput2]) -> _ToolResult[OrdersWeirdKeysOutput2]: ...
    @_typing.overload
    async def call_tool(self, name: _typing.Literal["émoji🎉"], options: _CallOptions | None = ..., /, **arguments: _typing.Unpack[OrdersMojiInput2]) -> _ToolResult[_builtins.str]: ...

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


_identity, _contracts = _json.loads('[{"name":"checkout","requires":{"123":{"name":"n","version":"1"},"_private":{"name":"p","version":"1"},"class":{"name":"c","version":"1"},"for":{"name":"keyword","version":"1"},"orders":{"name":"orders","version":"1.0.0"},"orders!":{"name":"example/orders","version":"2"},"pay-ments":{"name":"pay","version":"1"},"payments":{"name":"payments","version":"2.0.0"},"untyped_ok":{"name":"u","version":"1"},"with-hyphen":{"name":"h","version":"1"},"\\u00fcn\\u00ef":{"name":"\\u00fc","version":"1"}},"version":"1.0.0"},{"123":{"tools":[]},"_private":{"tools":[]},"for":{"tools":[{"name":"x","inputSchema":{"type":"object"}}]},"orders":{"tools":[{"name":"-","inputSchema":{"type":"object"}},{"name":"123","inputSchema":{"type":"object"}},{"name":"CallTool","inputSchema":{"type":"object"}},{"name":"call_tool","inputSchema":{"additionalProperties":false,"properties":{"a":{"type":"integer"},"b":{"type":"string"}},"required":["b"],"type":"object"},"outputSchema":{"additionalProperties":false,"properties":{"x":{"type":"boolean"}},"required":["x"],"type":"object"}},{"name":"class","inputSchema":{"additionalProperties":false,"properties":{"opt":{"additionalProperties":false,"properties":{"n":{"type":"number"}},"type":"object"}},"type":"object"},"outputSchema":{"items":{"$ref":"#/items"},"type":"array"}},{"name":"get-order","inputSchema":{"additionalProperties":false,"properties":{"id":{"type":"string"}},"required":["id"],"type":"object"}},{"name":"get_order","inputSchema":{"additionalProperties":false,"properties":{"id":{"type":"string"}},"required":["id"],"type":"object"}},{"name":"kitchen-sink","inputSchema":{"$defs":{"9lives":{"type":"string"},"Node":{"additionalProperties":false,"properties":{"next":{"$ref":"#/$defs/Node"},"value":{"type":"integer"}},"required":["value"],"type":"object"},"_private":{"type":"boolean"},"a/b~c":{"type":"integer"},"for":{"type":"string"},"list":{"prefixItems":[{"type":"string"}],"type":"array"}},"additionalProperties":false,"definitions":{"Legacy":{"type":"number"},"Node":{"type":"string"}},"properties":{"1starts":{"type":"string"},"Dash-Key":{"type":"string"},"all":{"allOf":[{"properties":{"a":{"type":"string"}},"required":["a"],"type":"object"},{"properties":{"b":{"type":"integer"}},"type":"object"}]},"allConflict":{"allOf":[{"properties":{"a":{"type":"string"}},"type":"object"},{"properties":{"a":{"type":"integer"}},"type":"object"}]},"allNonObject":{"allOf":[{"type":"string"},{"type":"object"}]},"annotated":{"$comment":"c","description":"gone","examples":[1],"title":"t","type":"string"},"arrayRef":{"$ref":"#/$defs/list/prefixItems/0"},"closedEmpty":{"additionalProperties":false,"type":"object"},"constNum":{"const":1e+21},"constObj":{"const":{"deep":[1,2]}},"constString":{"const":"it\'s \\"quoted\\"\\n"},"cycle":{"$ref":"#/$defs/Node"},"dash-key":{"type":"string"},"emptyAll":{"allOf":[]},"emptyAny":{"anyOf":[]},"falseSchema":false,"implicitTuple":{"minItems":2.0,"prefixItems":[{"const":"x"}],"type":"array"},"literals":{"enum":["a",1,2.5,-3,true,null,[1],{"k":"v"},1.0,1e+21,-0.0]},"mapOf":{"additionalProperties":{"type":"string"},"type":"object"},"multi":{"items":{"type":"integer"},"type":["string","null","array",5]},"nested":{"items":{"items":{"additionalProperties":false,"properties":{"deep":{"type":"null"}},"type":"object"},"type":"array"},"type":"array"},"noType":{"properties":{"p":{"type":"boolean"}},"required":["p",7]},"numericType":{"properties":{"p":{"type":"boolean"}},"type":7},"odd key with spaces":{"type":"string"},"oneOf":{"oneOf":[{"type":"string"},{"type":"number"}]},"openObj":{"additionalProperties":{"type":"integer"},"properties":{"x":{"type":"string"}},"type":"object"},"openTuple":{"additionalItems":{"type":"number"},"items":[{"type":"boolean"}],"type":"array"},"plainArray":{"type":"array"},"pointerEscape":{"$ref":"#/$defs/a~1b~0c"},"rootRef":{"$ref":"#"},"trueObj":{"additionalProperties":true,"properties":{"x":{"type":"string"}},"type":"object"},"trueSchema":true,"tuple":{"items":false,"minItems":1,"prefixItems":[{"type":"string"},{"type":"integer"}],"type":"array"},"unknownKeyword":{"format":"email"}},"required":["tuple","literals","noType"],"type":"object"}},{"name":"weird-keys","inputSchema":{"additionalProperties":false,"properties":{"":{"type":"null"},"-":{"type":"boolean"},"x\\"quote":{"type":"string"},"\\u00fcn\\u00efcode":{"type":"integer"}},"required":[""],"type":"object"},"outputSchema":{"additionalProperties":false,"properties":{"a.b":{"type":"string"},"nl\\nname":{"type":"string"},"tab\\tname":{"type":"string"}},"required":["tab\\tname"],"type":"object"}},{"name":"\\u00e9moji\\ud83c\\udf89","inputSchema":{"properties":{"\\u00e9":{"type":"string"}},"type":"object"},"outputSchema":{"description":"\\u00e9","type":"string"}}],"resources":[{"uri":"res://plain","outputSchema":{"type":"string"}},{"uri":"res://typed","outputSchema":{"$defs":{"Item":{"additionalProperties":false,"properties":{"id":{"type":"string"}},"required":["id"],"type":"object"}},"additionalProperties":false,"properties":{"items":{"items":{"$ref":"#/$defs/Item"},"type":"array"}},"required":["items"],"type":"object"}},{"uri":"res://\\u00e9/\\u00fc","outputSchema":{"properties":{"n":{"default":-0.0,"maximum":1e+21,"minimum":0.5,"multipleOf":1.0,"type":"number"}},"type":"object"}}]},"orders!":{"tools":[{"name":"-","inputSchema":{"type":"object"}},{"name":"123","inputSchema":{"type":"object"}},{"name":"CallTool","inputSchema":{"type":"object"}},{"name":"call_tool","inputSchema":{"additionalProperties":false,"properties":{"a":{"type":"integer"},"b":{"type":"string"}},"required":["b"],"type":"object"},"outputSchema":{"additionalProperties":false,"properties":{"x":{"type":"boolean"}},"required":["x"],"type":"object"}},{"name":"class","inputSchema":{"additionalProperties":false,"properties":{"opt":{"additionalProperties":false,"properties":{"n":{"type":"number"}},"type":"object"}},"type":"object"},"outputSchema":{"items":{"$ref":"#/items"},"type":"array"}},{"name":"get-order","inputSchema":{"additionalProperties":false,"properties":{"id":{"type":"string"}},"required":["id"],"type":"object"}},{"name":"get_order","inputSchema":{"additionalProperties":false,"properties":{"id":{"type":"string"}},"required":["id"],"type":"object"}},{"name":"kitchen-sink","inputSchema":{"$defs":{"9lives":{"type":"string"},"Node":{"additionalProperties":false,"properties":{"next":{"$ref":"#/$defs/Node"},"value":{"type":"integer"}},"required":["value"],"type":"object"},"_private":{"type":"boolean"},"a/b~c":{"type":"integer"},"for":{"type":"string"},"list":{"prefixItems":[{"type":"string"}],"type":"array"}},"additionalProperties":false,"definitions":{"Legacy":{"type":"number"},"Node":{"type":"string"}},"properties":{"1starts":{"type":"string"},"Dash-Key":{"type":"string"},"all":{"allOf":[{"properties":{"a":{"type":"string"}},"required":["a"],"type":"object"},{"properties":{"b":{"type":"integer"}},"type":"object"}]},"allConflict":{"allOf":[{"properties":{"a":{"type":"string"}},"type":"object"},{"properties":{"a":{"type":"integer"}},"type":"object"}]},"allNonObject":{"allOf":[{"type":"string"},{"type":"object"}]},"annotated":{"$comment":"c","description":"gone","examples":[1],"title":"t","type":"string"},"arrayRef":{"$ref":"#/$defs/list/prefixItems/0"},"closedEmpty":{"additionalProperties":false,"type":"object"},"constNum":{"const":1e+21},"constObj":{"const":{"deep":[1,2]}},"constString":{"const":"it\'s \\"quoted\\"\\n"},"cycle":{"$ref":"#/$defs/Node"},"dash-key":{"type":"string"},"emptyAll":{"allOf":[]},"emptyAny":{"anyOf":[]},"falseSchema":false,"implicitTuple":{"minItems":2.0,"prefixItems":[{"const":"x"}],"type":"array"},"literals":{"enum":["a",1,2.5,-3,true,null,[1],{"k":"v"},1.0,1e+21,-0.0]},"mapOf":{"additionalProperties":{"type":"string"},"type":"object"},"multi":{"items":{"type":"integer"},"type":["string","null","array",5]},"nested":{"items":{"items":{"additionalProperties":false,"properties":{"deep":{"type":"null"}},"type":"object"},"type":"array"},"type":"array"},"noType":{"properties":{"p":{"type":"boolean"}},"required":["p",7]},"numericType":{"properties":{"p":{"type":"boolean"}},"type":7},"odd key with spaces":{"type":"string"},"oneOf":{"oneOf":[{"type":"string"},{"type":"number"}]},"openObj":{"additionalProperties":{"type":"integer"},"properties":{"x":{"type":"string"}},"type":"object"},"openTuple":{"additionalItems":{"type":"number"},"items":[{"type":"boolean"}],"type":"array"},"plainArray":{"type":"array"},"pointerEscape":{"$ref":"#/$defs/a~1b~0c"},"rootRef":{"$ref":"#"},"trueObj":{"additionalProperties":true,"properties":{"x":{"type":"string"}},"type":"object"},"trueSchema":true,"tuple":{"items":false,"minItems":1,"prefixItems":[{"type":"string"},{"type":"integer"}],"type":"array"},"unknownKeyword":{"format":"email"}},"required":["tuple","literals","noType"],"type":"object"}},{"name":"weird-keys","inputSchema":{"additionalProperties":false,"properties":{"":{"type":"null"},"-":{"type":"boolean"},"x\\"quote":{"type":"string"},"\\u00fcn\\u00efcode":{"type":"integer"}},"required":[""],"type":"object"},"outputSchema":{"additionalProperties":false,"properties":{"a.b":{"type":"string"},"nl\\nname":{"type":"string"},"tab\\tname":{"type":"string"}},"required":["tab\\tname"],"type":"object"}},{"name":"\\u00e9moji\\ud83c\\udf89","inputSchema":{"properties":{"\\u00e9":{"type":"string"}},"type":"object"},"outputSchema":{"description":"\\u00e9","type":"string"}}],"resources":[{"uri":"res://plain","outputSchema":{"type":"string"}},{"uri":"res://typed","outputSchema":{"$defs":{"Item":{"additionalProperties":false,"properties":{"id":{"type":"string"}},"required":["id"],"type":"object"}},"additionalProperties":false,"properties":{"items":{"items":{"$ref":"#/$defs/Item"},"type":"array"}},"required":["items"],"type":"object"}},{"uri":"res://\\u00e9/\\u00fc","outputSchema":{"properties":{"n":{"default":-0.0,"maximum":1e+21,"minimum":0.5,"multipleOf":1.0,"type":"number"}},"type":"object"}}]},"with-hyphen":{"tools":[{"name":"y","inputSchema":{"type":"object"}}]},"\\u00fcn\\u00ef":{"tools":[{"name":"z","inputSchema":{"type":"object"}}]}}]')

apps = _typing.cast(_Clients, _Apps(_identity, _contracts))

def create_apps(bindings: _typing.Mapping[str, _Binding]) -> _Clients:
    return _typing.cast(_Clients, _Apps(_identity, _contracts, bindings))
