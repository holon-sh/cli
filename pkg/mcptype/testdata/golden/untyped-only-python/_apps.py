# Input alternatives share a result type; redundant overloads are harmless.
# mypy: disable-error-code=overload-cannot-match
from __future__ import annotations

import json as _json

from .client import Apps as _Apps
from .client import Binding as _Binding
from .client import CallOptions as _CallOptions
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

class PayMentsApp(_typing.Protocol):
    async def call_tool(self, name: _builtins.str, options: _CallOptions | None = ..., /, **arguments: _builtins.object) -> _ToolResult[_builtins.object]: ...

class PaymentsApp(_typing.Protocol):
    async def call_tool(self, name: _builtins.str, options: _CallOptions | None = ..., /, **arguments: _builtins.object) -> _ToolResult[_builtins.object]: ...

class _Clients(_typing.Protocol):
    payments: PaymentsApp
    @_typing.overload
    def __getitem__(self, name: _typing.Literal["pay-ments"]) -> PayMentsApp: ...
    @_typing.overload
    def __getitem__(self, name: _typing.Literal["payments"]) -> PaymentsApp: ...


_identity, _contracts = _json.loads('[{"name":"a","requires":{"pay-ments":{"name":"p","version":"1"},"payments":{"name":"payments","version":"1"}},"version":"1"},{}]')

apps = _typing.cast(_Clients, _Apps(_identity, _contracts))

def create_apps(bindings: _typing.Mapping[str, _Binding]) -> _Clients:
    return _typing.cast(_Clients, _Apps(_identity, _contracts, bindings))
