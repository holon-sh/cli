from __future__ import annotations

import json as _json

from mcp.types import InputResponses as _InputResponses
from mcp.types import RequestParamsMeta as _RequestParamsMeta

from .client import Apps as _Apps
from .client import Binding as _Binding
from .client import ToolResult as _ToolResult

import builtins as _builtins
import typing as _typing

class Inputs:
    pass

class Outputs:
    pass

class InputTypes:
    pass

class OutputTypes:
    pass

class _Clients(_typing.Protocol):
    pass

class _Untyped(_typing.Protocol):
    async def call_tool(self, name: _builtins.str, arguments: _typing.Mapping[_builtins.str, _builtins.object], *, timeout: _builtins.float | None = ..., input_responses: _InputResponses | None = ..., request_state: _builtins.str | None = ..., meta: _RequestParamsMeta | None = ...) -> _ToolResult[_builtins.object]: ...

class _AllClients(_Clients, _typing.Protocol):
    payments: _Untyped


_identity, _contracts = _json.loads('[{"name":"a","requires":{"pay-ments":{"name":"p","version":"1"},"payments":{"name":"payments","version":"1"}},"version":"1"},{}]')

apps = _typing.cast(_AllClients, _Apps(_identity, _contracts))

def create_apps(bindings: _typing.Mapping[str, _Binding]) -> _AllClients:
    return _typing.cast(_AllClients, _Apps(_identity, _contracts, bindings))
