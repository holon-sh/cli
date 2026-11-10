package mcptype

import (
	"fmt"
	"maps"
	"strconv"
	"strings"
)

// The generated holon Python namespace module imports the installed runtime.

const pythonHeader = `# Input alternatives share a result type; redundant overloads are harmless.
# mypy: disable-error-code=overload-cannot-match
from __future__ import annotations

import json as _json

from holon.client import Apps as _Apps
from holon.client import Binding as _Binding
from holon.client import CallOptions as _CallOptions
from holon.client import CompleteResult as _CompleteResult
from holon.client import ToolResult as _ToolResult`

// pythonClients creates the clients. The runtime names each method after its tool, so a method named
// otherwise is added here.
const pythonClients = `def _clients(bindings: _typing.Mapping[str, _Binding] | None = None) -> _Clients:
    apps = _Apps(_identity, _contracts, bindings)
    for binding in _methods if bindings is None else _methods.keys() & bindings:
        tools = apps[binding].tools
        for tool, method in _methods[binding].items():
            setattr(tools, method, getattr(tools, tool))
    return _typing.cast(_Clients, apps)`

func generatePythonPackage(request generateRequest, catalogs map[string]catalog) map[string]string {
	renamed := map[string]map[string]string{}
	for binding, tools := range catalogs {
		methods := pythonMethods(tools)
		maps.DeleteFunc(methods, func(tool, method string) bool { return tool == method })
		if len(methods) > 0 {
			renamed[binding] = methods
		}
	}
	// The Python runtime has no events yet, so its clients carry none.
	contracts := make(map[string]contract, len(request.contracts))
	for alias, source := range request.contracts {
		contracts[alias] = contract{Tools: source.Tools, Resources: source.Resources}
	}
	// Go's ASCII quoted literals also evaluate as Python strings.
	literal := strconv.QuoteToASCII(string(encode([]Value{request.caller(), contracts, renamed})))
	module := strings.Join([]string{
		pythonHeader,
		generatePython(catalogs),
		fmt.Sprintf("_identity, _contracts, _methods = _json.loads(%s)", literal),
		pythonClients,
		"apps = _clients()",
		"def create_apps(bindings: _typing.Mapping[str, _Binding]) -> _Clients:\n    return _clients(bindings)",
	}, "\n\n")
	return map[string]string{"__init__.py": module + "\n"}
}
