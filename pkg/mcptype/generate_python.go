package mcptype

import (
	"bytes"
	"fmt"
	"strings"
	"unicode/utf16"
)

// The generated holon Python package: _apps.py beside the runtime files.

const pythonHeader = `# Input alternatives share a result type; redundant overloads are harmless.
# mypy: disable-error-code=overload-cannot-match
from __future__ import annotations

import json as _json

from .client import Apps as _Apps
from .client import Binding as _Binding
from .client import CallOptions as _CallOptions
from .client import ToolResult as _ToolResult`

func generatePythonPackage(request generateRequest, catalogs map[string]catalog) map[string]string {
	declarations := generatePython(catalogs, request.untyped())
	var payload bytes.Buffer
	payload.WriteByte('[')
	request.writeCaller(&payload)
	payload.WriteByte(',')
	writeContracts(&payload, request.contracts)
	payload.WriteByte(']')
	// JSON object keys contain double quotes, so Python repr uses single quotes.
	// JSON already escaped C0 controls; DEL is the only remaining ASCII control.
	literal := "'" + strings.NewReplacer(`\`, `\\`, "'", `\'`, "\x7f", `\x7f`).Replace(asciiJSON(payload.String())) + "'"
	header := strings.ReplaceAll(pythonHeader, "from .client import", fmt.Sprintf("from %s import", valueOr(request.pythonRuntimeModule, ".client")))
	module := strings.Join([]string{
		header,
		declarations,
		fmt.Sprintf("_identity, _contracts = _json.loads(%s)", literal),
		"apps = _typing.cast(_Clients, _Apps(_identity, _contracts))",
		"def create_apps(bindings: _typing.Mapping[str, _Binding]) -> _Clients:\n    return _typing.cast(_Clients, _Apps(_identity, _contracts, bindings))",
	}, "\n\n")
	files := map[string]string{valueOr(request.pythonModule, "_apps.py"): module + "\n"}
	if !request.slot() {
		files["package.json"] = pythonDumps(request.packageFields)
	}
	return files
}

// pythonDumps is Python's json.dumps with ensure_ascii and its default separators.
func pythonDumps(value Value) string {
	switch value := value.(type) {
	case []Value:
		items := make([]string, len(value))
		for i, item := range value {
			items[i] = pythonDumps(item)
		}
		return "[" + strings.Join(items, ", ") + "]"
	case Object:
		fields := make([]string, 0, len(value))
		for _, name := range sortedKeys(value) {
			fields = append(fields, pythonQuote(name)+": "+pythonDumps(value[name]))
		}
		return "{" + strings.Join(fields, ", ") + "}"
	case string:
		return pythonQuote(value)
	}
	return string(encode(value))
}

func pythonQuote(text string) string {
	return asciiJSON(quoted(text))
}

// asciiJSON rewrites JSON text with every non-ASCII character as \u escapes, as ensure_ascii writes it.
func asciiJSON(text string) string {
	var out strings.Builder
	for _, r := range text {
		if r < 0x80 {
			out.WriteRune(r)
			continue
		}
		for _, unit := range utf16.Encode([]rune{r}) {
			fmt.Fprintf(&out, "\\u%04x", unit)
		}
	}
	return out.String()
}
