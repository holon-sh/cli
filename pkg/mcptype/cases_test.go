package mcptype

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeFiles(t *testing.T, root string, files map[string][]byte) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// generationCase is one package-generation request in its JSON wire form.
type generationCase struct {
	name, input string
	wantError   bool
}

func (c generationCase) run(t *testing.T) (map[string][]byte, error) {
	var request GenerateRequest
	if err := json.Unmarshal([]byte(c.input), &request); err != nil {
		t.Fatal(err)
	}
	return Generate(t.Context(), request)
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
        "nodes": {"$ref": "#/$defs/Nodes"},
        "pointerEscape": {"$ref": "#/$defs/a~1b~0c"},
        "arrayRef": {"$ref": "#/$defs/list/prefixItems/0"},
        "rootRef": {"$ref": "#"},
        "falseSchema": false,
        "trueSchema": true,
        "plainArray": {"type": "array"},
        "nested": {"type": "array", "items": {"type": "array", "items": {"type": "object", "properties": {"deep": {"type": "null"}}, "additionalProperties": false}}},
        "files": {"type": "array", "items": {"type": "object", "x-mcp-file": {"accept": ["image/png"], "maxSize": 1048576}, "required": ["id", "mimeType", "size"],
          "properties": {"id": {"type": "string"}, "name": {"type": "string"}, "mimeType": {"type": "string"}, "size": {"type": "integer"}, "data": {"type": "string"}}}},
        "odd key with spaces": {"type": "string"},
        "dash-key": {"type": "string"},
        "1starts": {"type": "string"},
        "Dash-Key": {"type": "string"},
        "unknownKeyword": {"format": "email"},
        "annotated": {"type": "string", "description": "gone", "title": "t", "examples": [1], "$comment": "c"}
      },
      "$defs": {
        "Node": {"type": "object", "properties": {"next": {"$ref": "#/$defs/Node"}, "value": {"type": "integer"}}, "required": ["value"], "additionalProperties": false},
        "Nodes": {"type": "array", "items": {"$ref": "#/$defs/Node"}},
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
    {"name": "runtime.run", "inputSchema": {"type": "object", "properties": {"code": {"type": "string"}}, "additionalProperties": false, "required": ["code"]}, "outputSchema": {"type": "object", "properties": {"stdout": {"type": "string"}}, "additionalProperties": false, "required": ["stdout"]}},
    {"name": "order.list", "inputSchema": {"type": "object"}},
    {"name": "order/list", "inputSchema": {"type": "object"}},
    {"name": "_hidden", "inputSchema": {"type": "object"}},
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

const sinkRequires = `{"orders": {"name": "orders", "version": "1.0.0"}, "orders!": {"name": "example/orders", "version": "2"}, "123": {"name": "n", "version": "1"}, "for": {"name": "keyword", "version": "1"}, "with-hyphen": {"name": "h", "version": "1"}, "_private": {"name": "p", "version": "1"}, "ünï": {"name": "ü", "version": "1"}}`

// The second orders alias exercises identifier collisions without repeating every schema case.
func sinkContracts() string {
	return `{"orders": ` + kitchenSink + `, "orders!": {"tools": [{"name": "kitchen-sink", "inputSchema": {"type":"object", "properties":{"id":{"type":"string"}}, "required":["id"], "additionalProperties":false}, "outputSchema":{"type":"boolean"}}]}, "123": {"tools": []}, "for": {"tools": [{"name": "x", "inputSchema": {"type": "object"}}]}, "with-hyphen": {"tools": [{"name": "y", "inputSchema": {"type": "object"}}], "resources": []}, "_private": {"tools": []}, "ünï": {"tools": [{"name": "z", "inputSchema": {"type": "object"}}]}}`
}

func generationCases(t *testing.T) []generationCase {
	t.Helper()
	var cases []generationCase
	add := func(name, input string) { cases = append(cases, generationCase{name: name, input: input}) }
	nested, err := os.ReadFile(filepath.Join("testdata", "nested-types.json"))
	if err != nil {
		t.Fatal(err)
	}
	const order = `{"tools": [
        {"name": "get_order", "inputSchema": {"type": "object", "additionalProperties": false,
            "properties": {"orderId": {"type": "string"}, "count": {"type": "integer"}}, "required": ["orderId"]},
         "outputSchema": {"type": "object", "properties": {"total": {"type": "number"}}, "required": ["total"]}},
        {"name": "then", "inputSchema": {"type": "object", "additionalProperties": false}}
    ], "resources": [{"uri": "orders://summary", "outputSchema": {"type": "object", "properties": {"total": {"type": "number"}}, "required": ["total"]}}]}`
	sampleContracts := `{"orders": ` + order + `, "with-hyphen": ` + order + `, "mysql": ` + string(nested) + `, "empty": {"tools": []}`
	for _, target := range []string{"browser", "go"} {
		add("sink-"+target, `{"target": "`+target+`", "identity": {"name": "chéck\"out", "version": "1.0.0-rc.1"}, "requires": `+sinkRequires+`, "contracts": `+sinkContracts()+`, "go_module": "example.com/é/runtime", "own": "orders", "banner": "holon-generated-apps-v1 app=chéck\"out"}`)
	}
	add("empty-go", `{"target": "go", "identity": {"name": "a", "version": "1"}, "go_module": "example.com/runtime"}`)
	for _, target := range []string{"node", "python"} {
		// sink-browser already renders the kitchen sink's TypeScript, so only Python renders it here.
		contracts := sampleContracts + `, "schemas": ` + kitchenSink + `}`
		if target == "node" {
			contracts = sampleContracts + `}`
		}
		add("slot-"+target, `{"target": "`+target+`", "identity": {"name": "checkout", "version": "1.0.0"}, "requires": {"orders": {"name": "orders", "version": "1.0.0"}}, "contracts": `+contracts+`, "banner": "holon-generated-apps-v1 app=checkout"}`)
	}
	for _, test := range []struct{ name, patch string }{
		{"go-module-missing", `"target": "go", "go_module": ""`},
		{"banner-newline", `"banner": "marker\nimport os"`},
		{"binding-empty", `"contracts": {"": {"tools": []}}`},
		{"bad-target", `"target": "wasm"`},
		{"identity-empty", `"identity": {"name": "", "version": "1"}`},
		{"duplicate-tool", `"contracts": {"x": {"tools": [{"name":"read", "inputSchema":{"type":"object"}}, {"name":"read", "inputSchema":{"type":"object"}}]}}`},
		{"output-null", `"contracts": {"x": {"tools": [{"name":"read", "inputSchema":{"type":"object"}, "outputSchema":null}]}}`},
		{"duplicate-resource", `"contracts": {"x": {"tools": [], "resources": [{"uri":"r://a", "outputSchema":{}}, {"uri":"r://a", "outputSchema":{}}]}}`},
		{"unresolved-ref", `"contracts": {"x": {"tools": [{"name": "t", "inputSchema": {"type": "object", "properties": {"p": {"$ref": "#/nope"}}}}]}}`},
		{"external-ref-in-cycle", `"contracts": {"x": {"tools": [{"name": "t", "inputSchema": {"type": "object", "properties": {"p": {"$ref": "#/$defs/A"}}, "$defs": {"A": {"anyOf": [{"$ref": "/$defs/B"}]}, "B": {"$ref": "#/$defs/A"}}}}]}}`},
		{"schema-number", `"contracts": {"x": {"tools": [{"name": "t", "inputSchema": {"type": "object", "properties": {"p": 5}}}]}}`},
	} {
		cases = append(cases, generationCase{name: "invalid-" + test.name, input: `{"target": "node", "identity": {"name": "a", "version": "1"}, "go_module": "example.com/runtime", ` + test.patch + `}`, wantError: true})
	}
	return cases
}
