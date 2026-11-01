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

func generatePythonPackage(request generateRequest, catalogs map[string]catalog) map[string]string {
	declarations := generatePython(catalogs)
	// Go's ASCII quoted literals also evaluate as Python strings.
	literal := strconv.QuoteToASCII(string(encode([]Value{request.caller(), request.contracts})))
	module := strings.Join([]string{
		pythonHeader,
		declarations,
		fmt.Sprintf("_identity, _contracts = _json.loads(%s)", literal),
		"apps = _typing.cast(_Clients, _Apps(_identity, _contracts))",
		"def create_apps(bindings: _typing.Mapping[str, _Binding]) -> _Clients:\n    return _typing.cast(_Clients, _Apps(_identity, _contracts, bindings))",
	}, "\n\n")
	return map[string]string{"holon/apps/__init__.py": module + "\n"}
}
