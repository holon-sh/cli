# Input alternatives share a result type; redundant overloads are harmless.
# mypy: disable-error-code=overload-cannot-match
from __future__ import annotations

import json as _json

from _holon.client_1 import Apps as _Apps
from _holon.client_1 import Binding as _Binding
from _holon.client_1 import CallOptions as _CallOptions
from _holon.client_1 import ToolResult as _ToolResult

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


_identity, _contracts = _json.loads('[{"name":"a","requires":{},"version":"1"},{}]')

apps = _typing.cast(_Clients, _Apps(_identity, _contracts))

def create_apps(bindings: _typing.Mapping[str, _Binding]) -> _Clients:
    return _typing.cast(_Clients, _Apps(_identity, _contracts, bindings))
