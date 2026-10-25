from __future__ import annotations

import json as _json

from mcp.types import InputResponses as _InputResponses
from mcp.types import RequestParamsMeta as _RequestParamsMeta

from .client import Apps as _Apps
from .client import Binding as _Binding
from .client import ToolResult as _ToolResult

import builtins as _builtins
import typing as _typing

_Shape0 = _typing.TypedDict("_Shape0", {"column": _builtins.str, "label": _builtins.str})

_Shape1 = _typing.TypedDict("_Shape1", {"columns": _typing.NotRequired[_builtins.list[_builtins.str] | _builtins.list[_Shape0] | None], "direction": _typing.NotRequired[_typing.Literal["asc"] | _typing.Literal["desc"]], "table": _builtins.str})

_Shape2 = _typing.TypedDict("_Shape2", {"value": _builtins.float})

_Shape3 = _typing.TypedDict("_Shape3", {"columns": _builtins.list[_Shape2]})

_Shape4 = _typing.TypedDict("_Shape4", {"column": _builtins.str, "label": _builtins.str})

_Shape5 = _typing.TypedDict("_Shape5", {"value": _builtins.float})

_Shape6 = _typing.TypedDict("_Shape6", {"value": _builtins.float})

_Shape7 = _typing.TypedDict("_Shape7", {"columns": _builtins.list[_Shape6]})

_Shape8 = _typing.TypedDict("_Shape8", {"count": _typing.NotRequired[_builtins.int], "orderId": _builtins.str})

_Shape9 = _typing.TypedDict("_Shape9", {"total": _builtins.float})

_Shape10 = _typing.TypedDict("_Shape10", {"count": _typing.NotRequired[_builtins.int], "orderId": _builtins.str})

_Shape11 = _typing.TypedDict("_Shape11", {"total": _builtins.float})

class Inputs:
    class empty:
        pass
    class mysql:
        select: _typing.TypeAlias = _Shape1
    class orders:
        get_order: _typing.TypeAlias = _Shape8
        then: _typing.TypeAlias = _builtins.dict[_builtins.str, _builtins.object]

class Outputs:
    class empty:
        pass
    class mysql:
        select: _typing.TypeAlias = _Shape3
    class orders:
        get_order: _typing.TypeAlias = _Shape9
        then: _typing.TypeAlias = _builtins.object

class InputTypes:
    class empty:
        pass
    class mysql:
        class select:
            Direction: _typing.TypeAlias = _typing.Literal["asc"] | _typing.Literal["desc"]
            LabeledColumn: _typing.TypeAlias = _Shape4
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
            LabeledColumn: _typing.TypeAlias = _Shape5
            Root: _typing.TypeAlias = _Shape7
    class orders:
        class get_order:
            pass
        class then:
            pass

class _Tools0(_typing.Protocol):
    pass

class _App0(_typing.Protocol):
    tools: _Tools0

class _Tools1(_typing.Protocol):
    async def select(self, arguments: _Shape1, *, timeout: _builtins.float | None = ...) -> _Shape3: ...

class _App1(_typing.Protocol):
    tools: _Tools1
    async def call_tool(self, name: _typing.Literal["select"], arguments: _Shape1, *, timeout: _builtins.float | None = ..., input_responses: _InputResponses | None = ..., request_state: _builtins.str | None = ..., meta: _RequestParamsMeta | None = ...) -> _ToolResult[_Shape3]: ...

class _Tools2(_typing.Protocol):
    async def get_order(self, arguments: _Shape8, *, timeout: _builtins.float | None = ...) -> _Shape9: ...
    async def then(self, arguments: _builtins.dict[_builtins.str, _builtins.object], *, timeout: _builtins.float | None = ...) -> _builtins.object: ...

class _App2(_typing.Protocol):
    tools: _Tools2
    @_typing.overload
    async def call_tool(self, name: _typing.Literal["get_order"], arguments: _Shape8, *, timeout: _builtins.float | None = ..., input_responses: _InputResponses | None = ..., request_state: _builtins.str | None = ..., meta: _RequestParamsMeta | None = ...) -> _ToolResult[_Shape9]: ...
    @_typing.overload
    async def call_tool(self, name: _typing.Literal["then"], arguments: _builtins.dict[_builtins.str, _builtins.object], *, timeout: _builtins.float | None = ..., input_responses: _InputResponses | None = ..., request_state: _builtins.str | None = ..., meta: _RequestParamsMeta | None = ...) -> _ToolResult[_builtins.object]: ...

class _Tools3(_typing.Protocol):
    async def get_order(self, arguments: _Shape10, *, timeout: _builtins.float | None = ...) -> _Shape11: ...
    async def then(self, arguments: _builtins.dict[_builtins.str, _builtins.object], *, timeout: _builtins.float | None = ...) -> _builtins.object: ...

class _App3(_typing.Protocol):
    tools: _Tools3
    @_typing.overload
    async def call_tool(self, name: _typing.Literal["get_order"], arguments: _Shape10, *, timeout: _builtins.float | None = ..., input_responses: _InputResponses | None = ..., request_state: _builtins.str | None = ..., meta: _RequestParamsMeta | None = ...) -> _ToolResult[_Shape11]: ...
    @_typing.overload
    async def call_tool(self, name: _typing.Literal["then"], arguments: _builtins.dict[_builtins.str, _builtins.object], *, timeout: _builtins.float | None = ..., input_responses: _InputResponses | None = ..., request_state: _builtins.str | None = ..., meta: _RequestParamsMeta | None = ...) -> _ToolResult[_builtins.object]: ...

class _Clients(_typing.Protocol):
    empty: _App0
    mysql: _App1
    orders: _App2
    @_typing.overload
    def __getitem__(self, name: _typing.Literal["empty"]) -> _App0: ...
    @_typing.overload
    def __getitem__(self, name: _typing.Literal["mysql"]) -> _App1: ...
    @_typing.overload
    def __getitem__(self, name: _typing.Literal["orders"]) -> _App2: ...
    @_typing.overload
    def __getitem__(self, name: _typing.Literal["with-hyphen"]) -> _App3: ...

class _Untyped(_typing.Protocol):
    async def call_tool(self, name: _builtins.str, arguments: _typing.Mapping[_builtins.str, _builtins.object], *, timeout: _builtins.float | None = ..., input_responses: _InputResponses | None = ..., request_state: _builtins.str | None = ..., meta: _RequestParamsMeta | None = ...) -> _ToolResult[_builtins.object]: ...

class _AllClients(_Clients, _typing.Protocol):
    payments: _Untyped


_identity, _contracts = _json.loads('[{"name":"checkout","requires":{"orders":{"name":"orders","version":"1.0.0"},"payments":{"name":"payments","version":"2.0.0"}},"version":"1.0.0"},{"empty":{"tools":[]},"mysql":{"tools":[{"name":"select","inputSchema":{"$defs":{"Direction":{"enum":["asc","desc"]},"LabeledColumn":{"additionalProperties":false,"properties":{"column":{"type":"string"},"label":{"type":"string"}},"required":["column","label"],"type":"object"}},"additionalProperties":false,"properties":{"columns":{"anyOf":[{"items":{"type":"string"},"type":"array"},{"items":{"$ref":"#/$defs/LabeledColumn"},"type":"array"},{"type":"null"}]},"direction":{"$ref":"#/$defs/Direction"},"table":{"type":"string"}},"required":["table"],"type":"object"},"outputSchema":{"$ref":"#/definitions/Root","definitions":{"LabeledColumn":{"additionalProperties":false,"properties":{"value":{"type":"number"}},"required":["value"],"type":"object"},"Root":{"additionalProperties":false,"properties":{"columns":{"items":{"$ref":"#/definitions/LabeledColumn"},"type":"array"}},"required":["columns"],"type":"object"}}}}]},"orders":{"tools":[{"name":"get_order","inputSchema":{"additionalProperties":false,"properties":{"count":{"type":"integer"},"orderId":{"type":"string"}},"required":["orderId"],"type":"object"},"outputSchema":{"properties":{"total":{"type":"number"}},"required":["total"],"type":"object"}},{"name":"then","inputSchema":{"additionalProperties":false,"type":"object"}}],"resources":[{"uri":"orders://summary","outputSchema":{"properties":{"total":{"type":"number"}},"required":["total"],"type":"object"}}]},"with-hyphen":{"tools":[{"name":"get_order","inputSchema":{"additionalProperties":false,"properties":{"count":{"type":"integer"},"orderId":{"type":"string"}},"required":["orderId"],"type":"object"},"outputSchema":{"properties":{"total":{"type":"number"}},"required":["total"],"type":"object"}},{"name":"then","inputSchema":{"additionalProperties":false,"type":"object"}}],"resources":[{"uri":"orders://summary","outputSchema":{"properties":{"total":{"type":"number"}},"required":["total"],"type":"object"}}]}}]')

apps = _typing.cast(_AllClients, _Apps(_identity, _contracts))

def create_apps(bindings: _typing.Mapping[str, _Binding]) -> _AllClients:
    return _typing.cast(_AllClients, _Apps(_identity, _contracts, bindings))
