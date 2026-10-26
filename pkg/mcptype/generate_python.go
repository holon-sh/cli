package mcptype

import (
	"bytes"
	"cmp"
	"fmt"
	"strconv"
	"strings"
)

// The generated holon Python namespace module imports the installed runtime.

const pythonHeader = `from __future__ import annotations

import json as _json

from mcp.types import InputResponses as _InputResponses
from mcp.types import RequestParamsMeta as _RequestParamsMeta

from .client import Apps as _Apps
from .client import Binding as _Binding
from .client import ToolResult as _ToolResult`

const pythonUntyped = `class _Untyped(_typing.Protocol):
    async def call_tool(self, name: _builtins.str, arguments: _typing.Mapping[_builtins.str, _builtins.object], *, timeout: _builtins.float | None = ..., input_responses: _InputResponses | None = ..., request_state: _builtins.str | None = ..., meta: _RequestParamsMeta | None = ...) -> _ToolResult[_builtins.object]: ...`

func generatePythonPackage(request generateRequest, catalogs map[string]catalog) map[string]string {
	declarations := generatePython(catalogs)
	var payload bytes.Buffer
	payload.WriteByte('[')
	request.writeCaller(&payload)
	payload.WriteByte(',')
	writeContracts(&payload, request.contracts)
	payload.WriteByte(']')
	// Go's ASCII quoted literals also evaluate as Python strings.
	literal := strconv.QuoteToASCII(payload.String())
	var members strings.Builder
	for _, alias := range request.untyped() {
		if pythonIdentifier(alias) {
			fmt.Fprintf(&members, "\n    %s: _Untyped", alias)
		}
	}
	clients, untyped := "_Clients", ""
	if members.Len() > 0 {
		clients = "_AllClients"
		untyped = fmt.Sprintf("\n%s\n\nclass _AllClients(_Clients, _typing.Protocol):%s\n", pythonUntyped, members.String())
	}
	header := strings.ReplaceAll(pythonHeader, "from .client import", fmt.Sprintf("from %s import", cmp.Or(request.PythonRuntimeModule, "holon.client")))
	module := strings.Join([]string{
		header,
		declarations + untyped,
		fmt.Sprintf("_identity, _contracts = _json.loads(%s)", literal),
		fmt.Sprintf("apps = _typing.cast(%s, _Apps(_identity, _contracts))", clients),
		fmt.Sprintf("def create_apps(bindings: _typing.Mapping[str, _Binding]) -> %s:\n    return _typing.cast(%s, _Apps(_identity, _contracts, bindings))", clients, clients),
	}, "\n\n")
	return map[string]string{cmp.Or(request.PythonModule, "holon/apps/__init__.py"): module + "\n"}
}
