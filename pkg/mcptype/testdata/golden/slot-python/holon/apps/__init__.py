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

class Outputs:
    class empty:
        pass
    class mysql:
        select: _typing.TypeAlias = MysqlSelectOutput
    class orders:
        get_order: _typing.TypeAlias = OrdersGetOrderOutput
        then: _typing.TypeAlias = _builtins.object

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
        class then:
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
    async def then(self, options: _CallOptions | None = ..., /, **arguments: _typing.Unpack[OrdersThenInput]) -> _builtins.object: ...

class OrdersApp(_typing.Protocol):
    tools: OrdersTools
    @_typing.overload
    async def call_tool(self, name: _typing.Literal["get_order"], options: _CallOptions | None = ..., /, **arguments: _typing.Unpack[OrdersGetOrderInput]) -> _ToolResult[OrdersGetOrderOutput]: ...
    @_typing.overload
    async def call_tool(self, name: _typing.Literal["then"], options: _CallOptions | None = ..., /, **arguments: _typing.Unpack[OrdersThenInput]) -> _ToolResult[_builtins.object]: ...

class WithHyphenTools(_typing.Protocol):
    async def get_order(self, options: _CallOptions | None = ..., /, **arguments: _typing.Unpack[WithHyphenGetOrderInput]) -> WithHyphenGetOrderOutput: ...
    async def then(self, options: _CallOptions | None = ..., /, **arguments: _typing.Unpack[WithHyphenThenInput]) -> _builtins.object: ...

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
    @_typing.overload
    def __getitem__(self, name: _typing.Literal["empty"]) -> EmptyApp: ...
    @_typing.overload
    def __getitem__(self, name: _typing.Literal["mysql"]) -> MysqlApp: ...
    @_typing.overload
    def __getitem__(self, name: _typing.Literal["orders"]) -> OrdersApp: ...
    @_typing.overload
    def __getitem__(self, name: _typing.Literal["with-hyphen"]) -> WithHyphenApp: ...


_identity, _contracts = _json.loads('[{"name":"checkout","requires":{"orders":{"name":"orders","version":"1.0.0"}},"version":"1.0.0"},{"empty":{"tools":[]},"mysql":{"tools":[{"name":"select","inputSchema":{"$defs":{"Direction":{"enum":["asc","desc"]},"LabeledColumn":{"additionalProperties":false,"properties":{"column":{"type":"string"},"label":{"type":"string"}},"required":["column","label"],"type":"object"}},"additionalProperties":false,"properties":{"columns":{"anyOf":[{"items":{"type":"string"},"type":"array"},{"items":{"$ref":"#/$defs/LabeledColumn"},"type":"array"},{"type":"null"}]},"direction":{"$ref":"#/$defs/Direction"},"table":{"type":"string"}},"required":["table"],"type":"object"},"outputSchema":{"$ref":"#/definitions/Root","definitions":{"LabeledColumn":{"additionalProperties":false,"properties":{"value":{"type":"number"}},"required":["value"],"type":"object"},"Root":{"additionalProperties":false,"properties":{"columns":{"items":{"$ref":"#/definitions/LabeledColumn"},"type":"array"}},"required":["columns"],"type":"object"}}}}]},"orders":{"tools":[{"name":"get_order","inputSchema":{"additionalProperties":false,"properties":{"count":{"type":"integer"},"orderId":{"type":"string"}},"required":["orderId"],"type":"object"},"outputSchema":{"properties":{"total":{"type":"number"}},"required":["total"],"type":"object"}},{"name":"then","inputSchema":{"additionalProperties":false,"type":"object"}}],"resources":[{"uri":"orders://summary","outputSchema":{"properties":{"total":{"type":"number"}},"required":["total"],"type":"object"}}]},"with-hyphen":{"tools":[{"name":"get_order","inputSchema":{"additionalProperties":false,"properties":{"count":{"type":"integer"},"orderId":{"type":"string"}},"required":["orderId"],"type":"object"},"outputSchema":{"properties":{"total":{"type":"number"}},"required":["total"],"type":"object"}},{"name":"then","inputSchema":{"additionalProperties":false,"type":"object"}}],"resources":[{"uri":"orders://summary","outputSchema":{"properties":{"total":{"type":"number"}},"required":["total"],"type":"object"}}]}}]')

apps = _typing.cast(_Clients, _Apps(_identity, _contracts))

def create_apps(bindings: _typing.Mapping[str, _Binding]) -> _Clients:
    return _typing.cast(_Clients, _Apps(_identity, _contracts, bindings))
