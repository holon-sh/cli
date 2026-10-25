package mcptype

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// parityCase is one compiler request in its JSON wire form, as the C, Node and Python bindings send it.
type parityCase struct {
	name, op, input string
	wantError       bool
}

// runCase answers a request exactly as the compiler's JSON entry points do, byte for byte.
func runCase(op, input string) (string, error) {
	value, err := decodeJSON([]byte(input))
	if err != nil {
		return "", err
	}
	switch op {
	case "canonical":
		canonical, digest, err := canonicalRequest(value)
		if err != nil {
			return "", err
		}
		output := Object{"canonical": canonical, "digest": digest}
		if canonical == nil {
			output["digest"] = nil
		}
		return string(encode(output)), nil
	case "changes":
		found, err := changesRequest(value)
		if err != nil {
			return "", err
		}
		records := make([]Value, len(found))
		for i, change := range found {
			records[i] = change.record()
		}
		return string(encode(Object{"changes": records})), nil
	default:
		request, err := decodeGenerateRequest(value)
		if err != nil {
			return "", err
		}
		files, err := generate(context.Background(), request)
		if err != nil {
			return "", err
		}
		encoded := make(Object, len(files))
		for path, data := range files {
			encoded[path] = base64.StdEncoding.EncodeToString(data)
		}
		return string(encode(Object{"files": encoded})), nil
	}
}

const kitchenSink = `{
  "tools": [
    {"name": "kitchen-sink", "inputSchema": {"type": "object", "additionalProperties": false, "required": ["tuple", "literals", "noType"],
      "properties": {
        "tuple": {"type": "array", "prefixItems": [{"type": "string"}, {"type": "integer"}], "items": false, "minItems": 1},
        "openTuple": {"type": "array", "items": [{"type": "boolean"}], "additionalItems": {"type": "number"}},
        "implicitTuple": {"type": "array", "prefixItems": [{"const": "x"}], "minItems": 2.0},
        "literals": {"enum": ["a", 1, 2.5, -3, true, null, [1], {"k": "v"}, 1.0, 1e21, -0]},
        "constObj": {"const": {"deep": [1, 2]}},
        "constNum": {"const": 1e21},
        "constString": {"const": "it's \"quoted\"\n"},
        "multi": {"type": ["string", "null", "array", 5], "items": {"type": "integer"}},
        "all": {"allOf": [{"type": "object", "properties": {"a": {"type": "string"}}, "required": ["a"]}, {"type": "object", "properties": {"b": {"type": "integer"}}}]},
        "allConflict": {"allOf": [{"type": "object", "properties": {"a": {"type": "string"}}}, {"type": "object", "properties": {"a": {"type": "integer"}}}]},
        "allNonObject": {"allOf": [{"type": "string"}, {"type": "object"}]},
        "emptyAll": {"allOf": []},
        "emptyAny": {"anyOf": []},
        "oneOf": {"oneOf": [{"type": "string"}, {"type": "number"}]},
        "mapOf": {"type": "object", "additionalProperties": {"type": "string"}},
        "openObj": {"type": "object", "properties": {"x": {"type": "string"}}, "additionalProperties": {"type": "integer"}},
        "trueObj": {"type": "object", "properties": {"x": {"type": "string"}}, "additionalProperties": true},
        "closedEmpty": {"type": "object", "additionalProperties": false},
        "noType": {"properties": {"p": {"type": "boolean"}}, "required": ["p", 7]},
        "numericType": {"type": 7, "properties": {"p": {"type": "boolean"}}},
        "cycle": {"$ref": "#/$defs/Node"},
        "pointerEscape": {"$ref": "#/$defs/a~1b~0c"},
        "arrayRef": {"$ref": "#/$defs/list/prefixItems/0"},
        "rootRef": {"$ref": "#"},
        "falseSchema": false,
        "trueSchema": true,
        "plainArray": {"type": "array"},
        "nested": {"type": "array", "items": {"type": "array", "items": {"type": "object", "properties": {"deep": {"type": "null"}}, "additionalProperties": false}}},
        "odd key with spaces": {"type": "string"},
        "dash-key": {"type": "string"},
        "1starts": {"type": "string"},
        "Dash-Key": {"type": "string"},
        "unknownKeyword": {"format": "email"},
        "annotated": {"type": "string", "description": "gone", "title": "t", "examples": [1], "$comment": "c"}
      },
      "$defs": {
        "Node": {"type": "object", "properties": {"next": {"$ref": "#/$defs/Node"}, "value": {"type": "integer"}}, "required": ["value"], "additionalProperties": false},
        "a/b~c": {"type": "integer"},
        "list": {"type": "array", "prefixItems": [{"type": "string"}]},
        "for": {"type": "string"},
        "_private": {"type": "boolean"},
        "9lives": {"type": "string"}
      },
      "definitions": {"Legacy": {"type": "number"}, "Node": {"type": "string"}}
    }},
    {"name": "weird-keys", "inputSchema": {"type": "object", "additionalProperties": false, "properties": {"x\"quote": {"type": "string"}, "ünïcode": {"type": "integer"}, "-": {"type": "boolean"}, "": {"type": "null"}}, "required": [""]},
     "outputSchema": {"type": "object", "additionalProperties": false, "properties": {"tab\tname": {"type": "string"}, "nl\nname": {"type": "string"}, "a.b": {"type": "string"}}, "required": ["tab\tname"]}},
    {"name": "class", "inputSchema": {"type": "object", "properties": {"opt": {"type": "object", "additionalProperties": false, "properties": {"n": {"type": "number"}}}}, "additionalProperties": false}, "outputSchema": {"type": "array", "items": {"$ref": "#/items"}}},
    {"name": "call_tool", "inputSchema": {"type": "object", "properties": {"b": {"type": "string"}, "a": {"type": "integer"}}, "required": ["b"], "additionalProperties": false}, "outputSchema": {"type": "object", "properties": {"x": {"type": "boolean"}}, "additionalProperties": false, "required": ["x"]}},
    {"name": "CallTool", "inputSchema": {"type": "object"}},
    {"name": "get-order", "inputSchema": {"type": "object", "properties": {"id": {"type": "string"}}, "additionalProperties": false, "required": ["id"]}},
    {"name": "get_order", "inputSchema": {"type": "object", "properties": {"id": {"type": "string"}}, "additionalProperties": false, "required": ["id"]}},
    {"name": "123", "inputSchema": {"type": "object"}},
    {"name": "-", "inputSchema": {"type": "object"}},
    {"name": "émoji🎉", "inputSchema": {"type": "object", "properties": {"é": {"type": "string"}}}, "outputSchema": {"type": "string", "description": "é"}}
  ],
  "resources": [
    {"uri": "res://é/ü", "outputSchema": {"type": "object", "properties": {"n": {"type": "number", "minimum": 0.5, "maximum": 1e21, "default": -0, "multipleOf": 1.0}}}},
    {"uri": "res://plain", "outputSchema": {"type": "string"}},
    {"uri": "res://typed", "outputSchema": {"type": "object", "additionalProperties": false, "properties": {"items": {"type": "array", "items": {"$ref": "#/$defs/Item"}}}, "required": ["items"], "$defs": {"Item": {"type": "object", "properties": {"id": {"type": "string"}}, "required": ["id"], "additionalProperties": false}}}}
  ]
}`

const floatSchema = `{"type": "number", "minimum": 0.5, "maximum": 1e21, "default": -0, "multipleOf": 1.0, "x": 1e16, "y": 1e15, "z": 0.00001234, "w": 0.000001234, "big": 18446744073709551616, "neg": -9223372036854775809, "u64max": 18446744073709551615, "i64min": -9223372036854775808, "pi": 3.141592653589793, "exp": 1E2, "small": 5e-324, "large": 1.7976931348623157e308, "enum": [1.0, 1, "1", true, null, [], {}, -0, 0, 0.0, 1e21, 100.0, 1E2], "required": ["b", "a", "a"], "type2": ["null", "string", "null"]}`

const sinkRequires = `{"orders": {"name": "orders", "version": "1.0.0"}, "orders!": {"name": "example/orders", "version": "2"}, "123": {"name": "n", "version": "1"}, "for": {"name": "keyword", "version": "1"}, "with-hyphen": {"name": "h", "version": "1"}, "_private": {"name": "p", "version": "1"}, "ünï": {"name": "ü", "version": "1"}, "payments": {"name": "payments", "version": "2.0.0"}, "pay-ments": {"name": "pay", "version": "1"}, "class": {"name": "c", "version": "1"}, "untyped_ok": {"name": "u", "version": "1"}}`

func sinkContracts() string {
	return `{"orders": ` + kitchenSink + `, "orders!": ` + kitchenSink + `, "123": {"tools": []}, "for": {"tools": [{"name": "x", "inputSchema": {"type": "object"}}]}, "with-hyphen": {"tools": [{"name": "y", "inputSchema": {"type": "object"}}], "resources": []}, "_private": {"tools": []}, "ünï": {"tools": [{"name": "z", "inputSchema": {"type": "object"}}]}}`
}

func parityCases(t *testing.T) []parityCase {
	t.Helper()
	var cases []parityCase
	add := func(name, op, input string) { cases = append(cases, parityCase{name: name, op: op, input: input}) }
	data, err := os.ReadFile(filepath.Join("testdata", "contract-core.json"))
	if err != nil {
		t.Fatal(err)
	}
	var vectors struct {
		Canonical []struct{ Input json.RawMessage } `json:"canonical"`
		Changes   []struct{ Input json.RawMessage } `json:"changes"`
		Invalid   []json.RawMessage                 `json:"invalid"`
	}
	if err := json.Unmarshal(data, &vectors); err != nil {
		t.Fatal(err)
	}
	for i, vector := range vectors.Canonical {
		add("vector-canonical-"+string(rune('a'+i)), "canonical", string(vector.Input))
	}
	for i, vector := range vectors.Changes {
		add("vector-changes-"+string(rune('a'+i)), "changes", string(vector.Input))
	}
	for i, input := range vectors.Invalid {
		cases = append(cases, parityCase{name: "vector-invalid-" + string(rune('a'+i)), op: "changes", input: string(input), wantError: true})
	}
	nested, err := os.ReadFile(filepath.Join("testdata", "nested-types.json"))
	if err != nil {
		t.Fatal(err)
	}
	add("canonical-floats", "canonical", `{"schema": `+floatSchema+`}`)
	add("canonical-sink", "canonical", `{"contract": `+kitchenSink+`}`)
	add("canonical-nested", "canonical", `{"contract": `+string(nested)+`}`)
	add("canonical-metadata-sink", "canonical", `{"metadata": {"version": 1, "identity": {"name": "é", "version": "1"}, "contract": `+kitchenSink+`, "requires": {"x": {"identity": {"name": "x", "version": "1"}, "contract": {"tools": []}}}}}`)
	add("canonical-unicode", "canonical", `{"schema": {"type": "object", "properties": {"é": {"type": "string", "description": " "}, "a\"b": {"type": "string"}, "tab\t": {"type": "string"}}, "required": ["é", "a\"b"], "enum": ["z", "é", "a", "é"]}}`)
	cases = append(cases, parityCase{name: "canonical-schema-not-object", op: "canonical", input: `{"schema": []}`, wantError: true})
	cases = append(cases, parityCase{name: "canonical-unknown-variant", op: "canonical", input: `{"other": {}}`, wantError: true})
	cases = append(cases, parityCase{name: "canonical-two-keys", op: "canonical", input: `{"schema": {}, "contract": {"tools": []}}`, wantError: true})
	cases = append(cases, parityCase{name: "canonical-version-float", op: "canonical", input: `{"metadata": {"version": 1.0, "identity": {"name": "a", "version": "1"}}}`, wantError: true})
	add("changes-sink-vs-nested", "changes", `{"expected": {"identity": {"name": "a", "version": "1"}, "contract": `+kitchenSink+`}, "published": {"version": 1, "identity": {"name": "a", "version": "1"}, "contract": `+string(nested)+`}}`)
	add("changes-sink-same", "changes", `{"expected": {"identity": {"name": "a", "version": "1"}, "contract": `+kitchenSink+`}, "published": {"version": 1, "identity": {"name": "a", "version": "1"}, "contract": `+kitchenSink+`}}`)
	add("changes-float-literals", "changes", `{"expected": {"identity": {"name": "a", "version": "1"}, "contract": {"tools": [{"name": "t", "inputSchema": {"type": "object", "minimum": 1}}, {"name": "u", "inputSchema": {"type": "object", "minimum": 1.0}, "outputSchema": {"type": "object"}}, {"name": "v", "inputSchema": {"type": "object"}, "outputSchema": {"type": "object"}}]}}, "published": {"version": 1, "identity": {"name": "a", "version": "1"}, "contract": {"tools": [{"name": "t", "inputSchema": {"type": "object", "minimum": 1.0}}, {"name": "u", "inputSchema": {"type": "object", "minimum": 1.0}}, {"name": "v", "inputSchema": {"type": "object"}, "outputSchema": {"type": "object", "description": "only docs"}}]}}}`)
	add("changes-no-published-contract", "changes", `{"expected": {"identity": {"name": "a", "version": "1"}, "contract": {"tools": []}}, "published": {"version": 1, "identity": {"name": "a", "version": "1"}}}`)
	add("changes-missing-published", "changes", `{"expected": {"identity": {"name": "a", "version": "1"}, "contract": {"tools": []}}}`)
	const protocol = "type AuthProvider = { token(): Promise<string> };\ntype ToolClient<I, O, R> = { tools: { [K in keyof I]: (input: I[K]) => Promise<K extends keyof O ? O[K] : unknown> }; resources: { [U in keyof R]: { read(): Promise<R[U]> } }; callTool<K extends keyof I & string>(request: { name: K; arguments: I[K] }): Promise<unknown> };"
	const order = `{"tools": [
        {"name": "get_order", "inputSchema": {"type": "object", "additionalProperties": false,
            "properties": {"orderId": {"type": "string"}, "count": {"type": "integer"}}, "required": ["orderId"]},
         "outputSchema": {"type": "object", "properties": {"total": {"type": "number"}}, "required": ["total"]}},
        {"name": "then", "inputSchema": {"type": "object", "additionalProperties": false}}
    ], "resources": [{"uri": "orders://summary", "outputSchema": {"type": "object", "properties": {"total": {"type": "number"}}, "required": ["total"]}}]}`
	legacyContracts := `{"orders": ` + order + `, "with-hyphen": ` + order + `, "mysql": ` + string(nested) + `, "empty": {"tools": []}}`
	// The CLI adds a browser app's own contract under the reserved key self.
	selfContracts := `{"orders": ` + order + `, "self": {"tools": [{"name": "status", "inputSchema": {"type": "object", "additionalProperties": false}}]}}`
	runtime := `{"runtime.mjs": "` + base64.StdEncoding.EncodeToString([]byte("export {};\n")) + `", "browser-runtime.mjs": "` + base64.StdEncoding.EncodeToString([]byte("export {};\n")) + `", "nested/bytes.dat": "` + base64.StdEncoding.EncodeToString([]byte{0, 128, 255, '\n'}) + `"}`
	for _, target := range []string{"node", "browser", "python", "go"} {
		add("legacy-"+target, "generate", `{"target": "`+target+`", "identity": {"name": "checkout", "version": "1.0.0"}, "requires": {"orders": {"name": "orders", "version": "1.0.0"}, "payments": {"name": "payments", "version": "2.0.0"}}, "contracts": `+legacyContracts+`, "runtime": `+runtime+`, "protocol": `+quoted(protocol)+`, "package_fields": {"holon": "holon-generated-apps-v1"}, "go_module": "example.com/runtime"}`)
		add("sink-"+target, "generate", `{"target": "`+target+`", "identity": {"name": "chéck\"out", "version": "1.0.0-rc.1"}, "requires": `+sinkRequires+`, "contracts": `+sinkContracts()+`, "runtime": `+runtime+`, "protocol": `+quoted(protocol)+`, "package_fields": {"holon": "holon-generated-apps-v1", "nested": {"list": [1, 2.5, "é", null, {"k": [true, false]}], "empty": {}, "emptyList": []}, "number": 1e21, "zero": -0, "float": 1.0}, "go_module": "example.com/é/runtime", "banner": "holon-generated-apps-v1 app=chéck\"out"}`)
		add("empty-"+target, "generate", `{"target": "`+target+`", "identity": {"name": "a", "version": "1"}, "go_module": "example.com/runtime"}`)
		add("untyped-only-"+target, "generate", `{"target": "`+target+`", "identity": {"name": "a", "version": "1"}, "requires": {"payments": {"name": "payments", "version": "1"}, "pay-ments": {"name": "p", "version": "1"}}, "go_module": "example.com/runtime", "package_fields": {}}`)
	}
	for _, target := range []string{"node", "browser", "python"} {
		options := `"runtime_specifier": "../runtime.mjs", "browser_runtime_specifier": "../browser-runtime.mjs", "protocol_specifier": "../protocol.js"`
		if target == "python" {
			options = `"python_module": "holon/apps/__init__.py", "python_runtime_module": "holon.client"`
		}
		add("slot-"+target, "generate", `{"target": "`+target+`", "identity": {"name": "checkout", "version": "1.0.0"}, "requires": {"orders": {"name": "orders", "version": "1.0.0"}}, "contracts": `+legacyContracts+`, "banner": "holon-generated-apps-v1 app=checkout", `+options+`}`)
		add("slot-sink-"+target, "generate", `{"target": "`+target+`", "identity": {"name": "checkout", "version": "1.0.0"}, "requires": `+sinkRequires+`, "contracts": `+sinkContracts()+`, "runtime": `+runtime+`, "banner": "holon-generated-apps-v1 app=checkout", `+options+`}`)
	}
	add("slot-browser-index", "generate", `{"target": "browser", "identity": {"name": "checkout", "version": "1.0.0"}, "requires": {"orders": {"name": "orders", "version": "1.0.0"}}, "contracts": `+selfContracts+`, "banner": "holon-generated-apps-v1 app=checkout", "browser_runtime_specifier": "../runtime.mjs", "protocol_specifier": "holon-core/protocol", "entry": "index"}`)
	add("slot-node-runtime-only", "generate", `{"target": "node", "identity": {"name": "a", "version": "1"}, "runtime_specifier": "@holon/apps/runtime.mjs", "protocol": "type X = 1;"}`)
	add("slot-browser-only", "generate", `{"target": "browser", "identity": {"name": "a", "version": "1"}, "browser_runtime_specifier": "./x.mjs"}`)
	add("slot-python-module-only", "generate", `{"target": "python", "identity": {"name": "a", "version": "1"}, "python_module": "apps.py"}`)
	add("slot-python-runtime-only", "generate", `{"target": "python", "identity": {"name": "a", "version": "1"}, "python_runtime_module": "_holon.client_1"}`)
	add("banner-runtime", "generate", `{"target": "node", "identity": {"name": "a", "version": "1"}, "banner": "generated by a caller", "runtime": {"runtime.mjs": "`+base64.StdEncoding.EncodeToString([]byte("export {};\n"))+`", "index.js": "`+base64.StdEncoding.EncodeToString([]byte("overwritten\n"))+`"}}`)
	for _, test := range []struct{ name, patch string }{
		{"go-module-missing", `"target": "go", "go_module": null`},
		{"runtime-escape", `"runtime": {"../escape": ""}`},
		{"runtime-bad-base64", `"runtime": {"file": "%%"}`},
		{"protocol-exclusive", `"protocol": "", "protocol_specifier": "../protocol.js"`},
		{"runtime-specifier-empty", `"runtime_specifier": ""`},
		{"browser-specifier-newline", `"browser_runtime_specifier": "bad\nmodule"`},
		{"protocol-specifier-blank", `"protocol_specifier": " "`},
		{"python-module-escape", `"python_module": "../escape.py"`},
		{"python-module-ext", `"python_module": "apps.json"`},
		{"python-runtime-relative", `"python_runtime_module": ".client"`},
		{"python-runtime-keyword", `"python_runtime_module": "holon.class"`},
		{"banner-newline", `"banner": "marker\nimport os"`},
		{"entry-empty", `"entry": ""`},
		{"entry-separator", `"entry": "a/b"`},
		{"binding-empty", `"contracts": {"": {"tools": []}}`},
		{"unknown-field", `"unknown": true`},
		{"bad-target", `"target": "wasm"`},
		{"identity-empty", `"identity": {"name": "", "version": "1"}`},
		{"requires-empty-identity", `"requires": {"x": {"name": "x", "version": ""}}`},
		{"contract-null", `"contracts": {"x": null}`},
		{"external-ref", `"contracts": {"x": {"tools": [{"name": "t", "inputSchema": {"type": "object", "properties": {"p": {"$ref": "https://example.com"}}}}]}}`},
		{"unresolved-ref", `"contracts": {"x": {"tools": [{"name": "t", "inputSchema": {"type": "object", "properties": {"p": {"$ref": "#/nope"}}}}]}}`},
		{"schema-number", `"contracts": {"x": {"tools": [{"name": "t", "inputSchema": {"type": "object", "properties": {"p": 5}}}]}}`},
		{"items-number", `"contracts": {"x": {"tools": [{"name": "t", "inputSchema": {"type": "object", "properties": {"p": {"type": "array", "items": 5}}}}]}}`},
	} {
		cases = append(cases, parityCase{name: "invalid-" + test.name, op: "generate", input: `{"target": "node", "identity": {"name": "a", "version": "1"}, "go_module": "example.com/runtime", ` + test.patch + `}`, wantError: true})
	}
	return cases
}
