# holon-generated-apps-v1 app=chéck"out
from __future__ import annotations

import json as _json

from mcp.types import InputResponses as _InputResponses
from mcp.types import RequestParamsMeta as _RequestParamsMeta

from holon.client import Apps as _Apps
from holon.client import Binding as _Binding
from holon.client import ToolResult as _ToolResult

import builtins as _builtins
import typing as _typing

_Shape0 = _typing.TypedDict("_Shape0", {"a": _typing.NotRequired[_builtins.int], "b": _builtins.str})

_Shape1 = _typing.TypedDict("_Shape1", {"x": _builtins.bool})

_Shape2 = _typing.TypedDict("_Shape2", {"n": _typing.NotRequired[_builtins.float]})

_Shape3 = _typing.TypedDict("_Shape3", {"opt": _typing.NotRequired[_Shape2]})

_Shape4 = _typing.TypedDict("_Shape4", {"id": _builtins.str})

_Shape5 = _typing.TypedDict("_Shape5", {"id": _builtins.str})

_Shape6 = _typing.TypedDict("_Shape6", {"a": _builtins.str, "b": _typing.NotRequired[_builtins.int]})

_Shape7 = _typing.TypedDict("_Shape7", {"next": _typing.NotRequired[_builtins.object], "value": _builtins.int})

_Shape8 = _typing.TypedDict("_Shape8", {"deep": _typing.NotRequired[None]})

_Shape9 = _typing.TypedDict("_Shape9", {"p": _builtins.bool})

_Shape10 = _typing.TypedDict("_Shape10", {"p": _typing.NotRequired[_builtins.bool]})

_Shape11 = _typing.TypedDict("_Shape11", {"a": _builtins.str, "b": _typing.NotRequired[_builtins.int]})

_Shape12 = _typing.TypedDict("_Shape12", {"next": _typing.NotRequired[_builtins.object], "value": _builtins.int})

_Shape13 = _typing.TypedDict("_Shape13", {"deep": _typing.NotRequired[None]})

_Shape14 = _typing.TypedDict("_Shape14", {"p": _builtins.bool})

_Shape15 = _typing.TypedDict("_Shape15", {"p": _typing.NotRequired[_builtins.bool]})

_Shape16 = _typing.TypedDict("_Shape16", {"1starts": _typing.NotRequired[_builtins.str], "Dash-Key": _typing.NotRequired[_builtins.str], "all": _typing.NotRequired[_Shape11], "allConflict": _typing.NotRequired[_builtins.object], "allNonObject": _typing.NotRequired[_builtins.object], "annotated": _typing.NotRequired[_builtins.str], "arrayRef": _typing.NotRequired[_builtins.str], "closedEmpty": _typing.NotRequired[_builtins.dict[_builtins.str, _builtins.object]], "constNum": _typing.NotRequired[_builtins.float], "constObj": _typing.NotRequired[_builtins.object], "constString": _typing.NotRequired[_typing.Literal["it's \"quoted\"\n"]], "cycle": _typing.NotRequired[_Shape12], "dash-key": _typing.NotRequired[_builtins.str], "emptyAll": _typing.NotRequired[_builtins.dict[_builtins.str, _builtins.object]], "emptyAny": _typing.NotRequired[_typing.Never], "falseSchema": _typing.NotRequired[_typing.Never], "implicitTuple": _typing.NotRequired[_builtins.list[_builtins.object]], "literals": _typing.Literal["a"] | _typing.Literal[1] | _builtins.float | _typing.Literal[-3] | _typing.Literal[True] | None | _builtins.object | _builtins.object | _builtins.float | _builtins.float | _builtins.float, "mapOf": _typing.NotRequired[_builtins.dict[_builtins.str, _builtins.str]], "multi": _typing.NotRequired[_builtins.str | None | _builtins.list[_builtins.int] | _builtins.object], "nested": _typing.NotRequired[_builtins.list[_builtins.list[_Shape13]]], "noType": _Shape14, "numericType": _typing.NotRequired[_Shape15], "odd key with spaces": _typing.NotRequired[_builtins.str], "oneOf": _typing.NotRequired[_builtins.str | _builtins.float], "openObj": _typing.NotRequired[_builtins.dict[_builtins.str, _builtins.object]], "openTuple": _typing.NotRequired[_builtins.list[_builtins.object]], "plainArray": _typing.NotRequired[_builtins.list[_builtins.object]], "pointerEscape": _typing.NotRequired[_builtins.int], "rootRef": _typing.NotRequired[_builtins.object], "trueObj": _typing.NotRequired[_builtins.dict[_builtins.str, _builtins.object]], "trueSchema": _typing.NotRequired[_builtins.object], "tuple": _builtins.list[_builtins.object], "unknownKeyword": _typing.NotRequired[_builtins.object]})

_Shape17 = _typing.TypedDict("_Shape17", {"1starts": _typing.NotRequired[_builtins.str], "Dash-Key": _typing.NotRequired[_builtins.str], "all": _typing.NotRequired[_Shape6], "allConflict": _typing.NotRequired[_builtins.object], "allNonObject": _typing.NotRequired[_builtins.object], "annotated": _typing.NotRequired[_builtins.str], "arrayRef": _typing.NotRequired[_builtins.str], "closedEmpty": _typing.NotRequired[_builtins.dict[_builtins.str, _builtins.object]], "constNum": _typing.NotRequired[_builtins.float], "constObj": _typing.NotRequired[_builtins.object], "constString": _typing.NotRequired[_typing.Literal["it's \"quoted\"\n"]], "cycle": _typing.NotRequired[_Shape7], "dash-key": _typing.NotRequired[_builtins.str], "emptyAll": _typing.NotRequired[_builtins.dict[_builtins.str, _builtins.object]], "emptyAny": _typing.NotRequired[_typing.Never], "falseSchema": _typing.NotRequired[_typing.Never], "implicitTuple": _typing.NotRequired[_builtins.list[_builtins.object]], "literals": _typing.Literal["a"] | _typing.Literal[1] | _builtins.float | _typing.Literal[-3] | _typing.Literal[True] | None | _builtins.object | _builtins.object | _builtins.float | _builtins.float | _builtins.float, "mapOf": _typing.NotRequired[_builtins.dict[_builtins.str, _builtins.str]], "multi": _typing.NotRequired[_builtins.str | None | _builtins.list[_builtins.int] | _builtins.object], "nested": _typing.NotRequired[_builtins.list[_builtins.list[_Shape8]]], "noType": _Shape9, "numericType": _typing.NotRequired[_Shape10], "odd key with spaces": _typing.NotRequired[_builtins.str], "oneOf": _typing.NotRequired[_builtins.str | _builtins.float], "openObj": _typing.NotRequired[_builtins.dict[_builtins.str, _builtins.object]], "openTuple": _typing.NotRequired[_builtins.list[_builtins.object]], "plainArray": _typing.NotRequired[_builtins.list[_builtins.object]], "pointerEscape": _typing.NotRequired[_builtins.int], "rootRef": _typing.NotRequired[_Shape16], "trueObj": _typing.NotRequired[_builtins.dict[_builtins.str, _builtins.object]], "trueSchema": _typing.NotRequired[_builtins.object], "tuple": _builtins.list[_builtins.object], "unknownKeyword": _typing.NotRequired[_builtins.object]})

_Shape18 = _typing.TypedDict("_Shape18", {"": None, "-": _typing.NotRequired[_builtins.bool], "x\"quote": _typing.NotRequired[_builtins.str], "ünïcode": _typing.NotRequired[_builtins.int]})

_Shape19 = _typing.TypedDict("_Shape19", {"a.b": _typing.NotRequired[_builtins.str], "nl\nname": _typing.NotRequired[_builtins.str], "tab\tname": _builtins.str})

_Shape20 = _typing.TypedDict("_Shape20", {"é": _typing.NotRequired[_builtins.str]})

_Shape21 = _typing.TypedDict("_Shape21", {"id": _builtins.str})

class Inputs:
    class orders:
        CallTool: _typing.TypeAlias = _builtins.dict[_builtins.str, _builtins.object]
        call_tool: _typing.TypeAlias = _Shape0
        get_order: _typing.TypeAlias = _Shape5

class Outputs:
    class orders:
        CallTool: _typing.TypeAlias = _builtins.object
        call_tool: _typing.TypeAlias = _Shape1
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

class _Tools0(_typing.Protocol):
    pass

class _App0(_typing.Protocol):
    tools: _Tools0

class _Tools1(_typing.Protocol):
    pass

class _App1(_typing.Protocol):
    tools: _Tools1

class _Tools2(_typing.Protocol):
    async def x(self, arguments: _builtins.dict[_builtins.str, _builtins.object], *, timeout: _builtins.float | None = ...) -> _builtins.object: ...

class _App2(_typing.Protocol):
    tools: _Tools2
    async def call_tool(self, name: _typing.Literal["x"], arguments: _builtins.dict[_builtins.str, _builtins.object], *, timeout: _builtins.float | None = ..., input_responses: _InputResponses | None = ..., request_state: _builtins.str | None = ..., meta: _RequestParamsMeta | None = ...) -> _ToolResult[_builtins.object]: ...

class _Tools3(_typing.Protocol):
    async def CallTool(self, arguments: _builtins.dict[_builtins.str, _builtins.object], *, timeout: _builtins.float | None = ...) -> _builtins.object: ...
    async def call_tool(self, arguments: _Shape0, *, timeout: _builtins.float | None = ...) -> _Shape1: ...
    async def get_order(self, arguments: _Shape5, *, timeout: _builtins.float | None = ...) -> _builtins.object: ...

class _App3(_typing.Protocol):
    tools: _Tools3
    @_typing.overload
    async def call_tool(self, name: _typing.Literal["-"], arguments: _builtins.dict[_builtins.str, _builtins.object], *, timeout: _builtins.float | None = ..., input_responses: _InputResponses | None = ..., request_state: _builtins.str | None = ..., meta: _RequestParamsMeta | None = ...) -> _ToolResult[_builtins.object]: ...
    @_typing.overload
    async def call_tool(self, name: _typing.Literal["123"], arguments: _builtins.dict[_builtins.str, _builtins.object], *, timeout: _builtins.float | None = ..., input_responses: _InputResponses | None = ..., request_state: _builtins.str | None = ..., meta: _RequestParamsMeta | None = ...) -> _ToolResult[_builtins.object]: ...
    @_typing.overload
    async def call_tool(self, name: _typing.Literal["CallTool"], arguments: _builtins.dict[_builtins.str, _builtins.object], *, timeout: _builtins.float | None = ..., input_responses: _InputResponses | None = ..., request_state: _builtins.str | None = ..., meta: _RequestParamsMeta | None = ...) -> _ToolResult[_builtins.object]: ...
    @_typing.overload
    async def call_tool(self, name: _typing.Literal["call_tool"], arguments: _Shape0, *, timeout: _builtins.float | None = ..., input_responses: _InputResponses | None = ..., request_state: _builtins.str | None = ..., meta: _RequestParamsMeta | None = ...) -> _ToolResult[_Shape1]: ...
    @_typing.overload
    async def call_tool(self, name: _typing.Literal["class"], arguments: _Shape3, *, timeout: _builtins.float | None = ..., input_responses: _InputResponses | None = ..., request_state: _builtins.str | None = ..., meta: _RequestParamsMeta | None = ...) -> _ToolResult[_builtins.list[_builtins.object]]: ...
    @_typing.overload
    async def call_tool(self, name: _typing.Literal["get-order"], arguments: _Shape4, *, timeout: _builtins.float | None = ..., input_responses: _InputResponses | None = ..., request_state: _builtins.str | None = ..., meta: _RequestParamsMeta | None = ...) -> _ToolResult[_builtins.object]: ...
    @_typing.overload
    async def call_tool(self, name: _typing.Literal["get_order"], arguments: _Shape5, *, timeout: _builtins.float | None = ..., input_responses: _InputResponses | None = ..., request_state: _builtins.str | None = ..., meta: _RequestParamsMeta | None = ...) -> _ToolResult[_builtins.object]: ...
    @_typing.overload
    async def call_tool(self, name: _typing.Literal["kitchen-sink"], arguments: _Shape17, *, timeout: _builtins.float | None = ..., input_responses: _InputResponses | None = ..., request_state: _builtins.str | None = ..., meta: _RequestParamsMeta | None = ...) -> _ToolResult[_builtins.object]: ...
    @_typing.overload
    async def call_tool(self, name: _typing.Literal["weird-keys"], arguments: _Shape18, *, timeout: _builtins.float | None = ..., input_responses: _InputResponses | None = ..., request_state: _builtins.str | None = ..., meta: _RequestParamsMeta | None = ...) -> _ToolResult[_Shape19]: ...
    @_typing.overload
    async def call_tool(self, name: _typing.Literal["émoji🎉"], arguments: _Shape20, *, timeout: _builtins.float | None = ..., input_responses: _InputResponses | None = ..., request_state: _builtins.str | None = ..., meta: _RequestParamsMeta | None = ...) -> _ToolResult[_builtins.str]: ...

class _Tools4(_typing.Protocol):
    pass

class _App4(_typing.Protocol):
    tools: _Tools4
    async def call_tool(self, name: _typing.Literal["kitchen-sink"], arguments: _Shape21, *, timeout: _builtins.float | None = ..., input_responses: _InputResponses | None = ..., request_state: _builtins.str | None = ..., meta: _RequestParamsMeta | None = ...) -> _ToolResult[_builtins.bool]: ...

class _Tools5(_typing.Protocol):
    async def y(self, arguments: _builtins.dict[_builtins.str, _builtins.object], *, timeout: _builtins.float | None = ...) -> _builtins.object: ...

class _App5(_typing.Protocol):
    tools: _Tools5
    async def call_tool(self, name: _typing.Literal["y"], arguments: _builtins.dict[_builtins.str, _builtins.object], *, timeout: _builtins.float | None = ..., input_responses: _InputResponses | None = ..., request_state: _builtins.str | None = ..., meta: _RequestParamsMeta | None = ...) -> _ToolResult[_builtins.object]: ...

class _Tools6(_typing.Protocol):
    async def z(self, arguments: _builtins.dict[_builtins.str, _builtins.object], *, timeout: _builtins.float | None = ...) -> _builtins.object: ...

class _App6(_typing.Protocol):
    tools: _Tools6
    async def call_tool(self, name: _typing.Literal["z"], arguments: _builtins.dict[_builtins.str, _builtins.object], *, timeout: _builtins.float | None = ..., input_responses: _InputResponses | None = ..., request_state: _builtins.str | None = ..., meta: _RequestParamsMeta | None = ...) -> _ToolResult[_builtins.object]: ...

class _Clients(_typing.Protocol):
    orders: _App3
    @_typing.overload
    def __getitem__(self, name: _typing.Literal["123"]) -> _App0: ...
    @_typing.overload
    def __getitem__(self, name: _typing.Literal["_private"]) -> _App1: ...
    @_typing.overload
    def __getitem__(self, name: _typing.Literal["for"]) -> _App2: ...
    @_typing.overload
    def __getitem__(self, name: _typing.Literal["orders"]) -> _App3: ...
    @_typing.overload
    def __getitem__(self, name: _typing.Literal["orders!"]) -> _App4: ...
    @_typing.overload
    def __getitem__(self, name: _typing.Literal["with-hyphen"]) -> _App5: ...
    @_typing.overload
    def __getitem__(self, name: _typing.Literal["ünï"]) -> _App6: ...

class _Untyped(_typing.Protocol):
    async def call_tool(self, name: _builtins.str, arguments: _typing.Mapping[_builtins.str, _builtins.object], *, timeout: _builtins.float | None = ..., input_responses: _InputResponses | None = ..., request_state: _builtins.str | None = ..., meta: _RequestParamsMeta | None = ...) -> _ToolResult[_builtins.object]: ...

class _AllClients(_Clients, _typing.Protocol):
    payments: _Untyped
    untyped_ok: _Untyped


_identity, _contracts = _json.loads("[{\"name\":\"ch\u00e9ck\\\"out\",\"requires\":{\"123\":{\"name\":\"n\",\"version\":\"1\"},\"_private\":{\"name\":\"p\",\"version\":\"1\"},\"class\":{\"name\":\"c\",\"version\":\"1\"},\"for\":{\"name\":\"keyword\",\"version\":\"1\"},\"orders\":{\"name\":\"orders\",\"version\":\"1.0.0\"},\"orders!\":{\"name\":\"example/orders\",\"version\":\"2\"},\"pay-ments\":{\"name\":\"pay\",\"version\":\"1\"},\"payments\":{\"name\":\"payments\",\"version\":\"2.0.0\"},\"untyped_ok\":{\"name\":\"u\",\"version\":\"1\"},\"with-hyphen\":{\"name\":\"h\",\"version\":\"1\"},\"\u00fcn\u00ef\":{\"name\":\"\u00fc\",\"version\":\"1\"}},\"version\":\"1.0.0-rc.1\"},{\"123\":{\"tools\":[]},\"_private\":{\"tools\":[]},\"for\":{\"tools\":[{\"inputSchema\":{\"type\":\"object\"},\"name\":\"x\"}]},\"orders\":{\"resources\":[{\"outputSchema\":{\"type\":\"string\"},\"uri\":\"res://plain\"},{\"outputSchema\":{\"$defs\":{\"Item\":{\"additionalProperties\":false,\"properties\":{\"id\":{\"type\":\"string\"}},\"required\":[\"id\"],\"type\":\"object\"}},\"additionalProperties\":false,\"properties\":{\"items\":{\"items\":{\"$ref\":\"#/$defs/Item\"},\"type\":\"array\"}},\"required\":[\"items\"],\"type\":\"object\"},\"uri\":\"res://typed\"},{\"outputSchema\":{\"properties\":{\"n\":{\"default\":-0.0,\"maximum\":1e+21,\"minimum\":0.5,\"multipleOf\":1.0,\"type\":\"number\"}},\"type\":\"object\"},\"uri\":\"res://\u00e9/\u00fc\"}],\"tools\":[{\"inputSchema\":{\"type\":\"object\"},\"name\":\"-\"},{\"inputSchema\":{\"type\":\"object\"},\"name\":\"123\"},{\"inputSchema\":{\"type\":\"object\"},\"name\":\"CallTool\"},{\"inputSchema\":{\"additionalProperties\":false,\"properties\":{\"a\":{\"type\":\"integer\"},\"b\":{\"type\":\"string\"}},\"required\":[\"b\"],\"type\":\"object\"},\"name\":\"call_tool\",\"outputSchema\":{\"additionalProperties\":false,\"properties\":{\"x\":{\"type\":\"boolean\"}},\"required\":[\"x\"],\"type\":\"object\"}},{\"inputSchema\":{\"additionalProperties\":false,\"properties\":{\"opt\":{\"additionalProperties\":false,\"properties\":{\"n\":{\"type\":\"number\"}},\"type\":\"object\"}},\"type\":\"object\"},\"name\":\"class\",\"outputSchema\":{\"items\":{\"$ref\":\"#/items\"},\"type\":\"array\"}},{\"inputSchema\":{\"additionalProperties\":false,\"properties\":{\"id\":{\"type\":\"string\"}},\"required\":[\"id\"],\"type\":\"object\"},\"name\":\"get-order\"},{\"inputSchema\":{\"additionalProperties\":false,\"properties\":{\"id\":{\"type\":\"string\"}},\"required\":[\"id\"],\"type\":\"object\"},\"name\":\"get_order\"},{\"inputSchema\":{\"$defs\":{\"9lives\":{\"type\":\"string\"},\"Node\":{\"additionalProperties\":false,\"properties\":{\"next\":{\"$ref\":\"#/$defs/Node\"},\"value\":{\"type\":\"integer\"}},\"required\":[\"value\"],\"type\":\"object\"},\"_private\":{\"type\":\"boolean\"},\"a/b~c\":{\"type\":\"integer\"},\"for\":{\"type\":\"string\"},\"list\":{\"prefixItems\":[{\"type\":\"string\"}],\"type\":\"array\"}},\"additionalProperties\":false,\"definitions\":{\"Legacy\":{\"type\":\"number\"},\"Node\":{\"type\":\"string\"}},\"properties\":{\"1starts\":{\"type\":\"string\"},\"Dash-Key\":{\"type\":\"string\"},\"all\":{\"allOf\":[{\"properties\":{\"a\":{\"type\":\"string\"}},\"required\":[\"a\"],\"type\":\"object\"},{\"properties\":{\"b\":{\"type\":\"integer\"}},\"type\":\"object\"}]},\"allConflict\":{\"allOf\":[{\"properties\":{\"a\":{\"type\":\"string\"}},\"type\":\"object\"},{\"properties\":{\"a\":{\"type\":\"integer\"}},\"type\":\"object\"}]},\"allNonObject\":{\"allOf\":[{\"type\":\"string\"},{\"type\":\"object\"}]},\"annotated\":{\"$comment\":\"c\",\"description\":\"gone\",\"examples\":[1],\"title\":\"t\",\"type\":\"string\"},\"arrayRef\":{\"$ref\":\"#/$defs/list/prefixItems/0\"},\"closedEmpty\":{\"additionalProperties\":false,\"type\":\"object\"},\"constNum\":{\"const\":1e+21},\"constObj\":{\"const\":{\"deep\":[1,2]}},\"constString\":{\"const\":\"it's \\\"quoted\\\"\\n\"},\"cycle\":{\"$ref\":\"#/$defs/Node\"},\"dash-key\":{\"type\":\"string\"},\"emptyAll\":{\"allOf\":[]},\"emptyAny\":{\"anyOf\":[]},\"falseSchema\":false,\"implicitTuple\":{\"minItems\":2.0,\"prefixItems\":[{\"const\":\"x\"}],\"type\":\"array\"},\"literals\":{\"enum\":[\"a\",1,2.5,-3,true,null,[1],{\"k\":\"v\"},1.0,1e+21,-0.0]},\"mapOf\":{\"additionalProperties\":{\"type\":\"string\"},\"type\":\"object\"},\"multi\":{\"items\":{\"type\":\"integer\"},\"type\":[\"string\",\"null\",\"array\",5]},\"nested\":{\"items\":{\"items\":{\"additionalProperties\":false,\"properties\":{\"deep\":{\"type\":\"null\"}},\"type\":\"object\"},\"type\":\"array\"},\"type\":\"array\"},\"noType\":{\"properties\":{\"p\":{\"type\":\"boolean\"}},\"required\":[\"p\",7]},\"numericType\":{\"properties\":{\"p\":{\"type\":\"boolean\"}},\"type\":7},\"odd key with spaces\":{\"type\":\"string\"},\"oneOf\":{\"oneOf\":[{\"type\":\"string\"},{\"type\":\"number\"}]},\"openObj\":{\"additionalProperties\":{\"type\":\"integer\"},\"properties\":{\"x\":{\"type\":\"string\"}},\"type\":\"object\"},\"openTuple\":{\"additionalItems\":{\"type\":\"number\"},\"items\":[{\"type\":\"boolean\"}],\"type\":\"array\"},\"plainArray\":{\"type\":\"array\"},\"pointerEscape\":{\"$ref\":\"#/$defs/a~1b~0c\"},\"rootRef\":{\"$ref\":\"#\"},\"trueObj\":{\"additionalProperties\":true,\"properties\":{\"x\":{\"type\":\"string\"}},\"type\":\"object\"},\"trueSchema\":true,\"tuple\":{\"items\":false,\"minItems\":1,\"prefixItems\":[{\"type\":\"string\"},{\"type\":\"integer\"}],\"type\":\"array\"},\"unknownKeyword\":{\"format\":\"email\"}},\"required\":[\"tuple\",\"literals\",\"noType\"],\"type\":\"object\"},\"name\":\"kitchen-sink\"},{\"inputSchema\":{\"additionalProperties\":false,\"properties\":{\"\":{\"type\":\"null\"},\"-\":{\"type\":\"boolean\"},\"x\\\"quote\":{\"type\":\"string\"},\"\u00fcn\u00efcode\":{\"type\":\"integer\"}},\"required\":[\"\"],\"type\":\"object\"},\"name\":\"weird-keys\",\"outputSchema\":{\"additionalProperties\":false,\"properties\":{\"a.b\":{\"type\":\"string\"},\"nl\\nname\":{\"type\":\"string\"},\"tab\\tname\":{\"type\":\"string\"}},\"required\":[\"tab\\tname\"],\"type\":\"object\"}},{\"inputSchema\":{\"properties\":{\"\u00e9\":{\"type\":\"string\"}},\"type\":\"object\"},\"name\":\"\u00e9moji\U0001f389\",\"outputSchema\":{\"description\":\"\u00e9\",\"type\":\"string\"}}]},\"orders!\":{\"tools\":[{\"inputSchema\":{\"additionalProperties\":false,\"properties\":{\"id\":{\"type\":\"string\"}},\"required\":[\"id\"],\"type\":\"object\"},\"name\":\"kitchen-sink\",\"outputSchema\":{\"type\":\"boolean\"}}]},\"with-hyphen\":{\"tools\":[{\"inputSchema\":{\"type\":\"object\"},\"name\":\"y\"}]},\"\u00fcn\u00ef\":{\"tools\":[{\"inputSchema\":{\"type\":\"object\"},\"name\":\"z\"}]}}]")

apps = _typing.cast(_AllClients, _Apps(_identity, _contracts))

def create_apps(bindings: _typing.Mapping[str, _Binding]) -> _AllClients:
    return _typing.cast(_AllClients, _Apps(_identity, _contracts, bindings))
