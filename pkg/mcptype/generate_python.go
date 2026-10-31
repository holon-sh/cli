package mcptype

import (
	"cmp"
	"fmt"
	"strconv"
	"strings"
)

// The generated holon Python namespace module imports the installed runtime.

const pythonHeader = `# Input alternatives share a result type; redundant overloads are harmless.
# mypy: disable-error-code=overload-cannot-match
from __future__ import annotations

import json as _json

from .client import Apps as _Apps
from .client import Binding as _Binding
from .client import CallOptions as _CallOptions
from .client import CompleteResult as _CompleteResult
from .client import ToolResult as _ToolResult`

func generatePythonPackage(request generateRequest, catalogs map[string]catalog) map[string]string {
	declarations := generatePython(catalogs)
	// Go's ASCII quoted literals also evaluate as Python strings.
	literal := strconv.QuoteToASCII(string(encode([]Value{request.caller(), request.contracts})))
	header := strings.ReplaceAll(pythonHeader, "from .client import", fmt.Sprintf("from %s import", cmp.Or(request.PythonRuntimeModule, "holon.client")))
	module := strings.Join([]string{
		header,
		declarations,
		fmt.Sprintf("_identity, _contracts = _json.loads(%s)", literal),
		"apps = _typing.cast(_Clients, _Apps(_identity, _contracts))",
		"def create_apps(bindings: _typing.Mapping[str, _Binding]) -> _Clients:\n    return _typing.cast(_Clients, _Apps(_identity, _contracts, bindings))",
	}, "\n\n")
	return map[string]string{cmp.Or(request.PythonModule, "holon/apps/__init__.py"): module + "\n"}
}
