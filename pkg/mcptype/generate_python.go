package mcptype

import (
	"fmt"
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

// pythonRenaming adds methods named other than their tools to each client's tools, since the runtime
// names its methods after the tools.
const pythonRenaming = `def _clients(apps: _Apps) -> _Clients:
    for binding, methods in _methods.items():
        try:
            tools = apps[binding].tools
        except KeyError:
            continue
        for method, tool in methods.items():
            setattr(tools, method, getattr(tools, tool))
    return _typing.cast(_Clients, apps)`

func generatePythonPackage(request generateRequest, catalogs map[string]catalog) map[string]string {
	declarations, renamed := generatePython(catalogs)
	// Go's ASCII quoted literals also evaluate as Python strings.
	literal := strconv.QuoteToASCII(string(encode([]Value{request.caller(), request.contracts})))
	sections := []string{pythonHeader, declarations, fmt.Sprintf("_identity, _contracts = _json.loads(%s)", literal)}
	clients := "_typing.cast(_Clients, %s)"
	if len(renamed) > 0 {
		methods := strconv.QuoteToASCII(string(encode(renamed)))
		sections = append(sections, fmt.Sprintf("_methods: dict[str, dict[str, str]] = _json.loads(%s)", methods), pythonRenaming)
		clients = "_clients(%s)"
	}
	sections = append(sections,
		"apps = "+fmt.Sprintf(clients, "_Apps(_identity, _contracts)"),
		"def create_apps(bindings: _typing.Mapping[str, _Binding]) -> _Clients:\n    return "+fmt.Sprintf(clients, "_Apps(_identity, _contracts, bindings)"),
	)
	return map[string]string{"holon/apps/__init__.py": strings.Join(sections, "\n\n") + "\n"}
}
