package mcptype

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The compiler's shared test vectors: canonical forms, digests, change records and rejected documents.
func TestSharedVectorsMatch(t *testing.T) {
	var vectors struct {
		Canonical []struct{ Input, Output json.RawMessage } `json:"canonical"`
		Changes   []struct{ Input, Output json.RawMessage } `json:"changes"`
		Invalid   []json.RawMessage                         `json:"invalid"`
	}
	data, err := os.ReadFile(filepath.Join("testdata", "contract-core.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &vectors); err != nil {
		t.Fatal(err)
	}
	for _, vector := range vectors.Canonical {
		input, err := decodeJSON(vector.Input)
		if err != nil {
			t.Fatal(err)
		}
		canonical, digest, err := canonicalRequest(input)
		if err != nil {
			t.Fatalf("%s: %v", vector.Input, err)
		}
		output := Object{"canonical": canonical, "digest": digest}
		if canonical == nil {
			output["digest"] = nil
		}
		if !sameJSON(t, encode(output), vector.Output) {
			t.Errorf("%s:\n got %s\nwant %s", vector.Input, encode(output), vector.Output)
		}
	}
	for _, vector := range vectors.Changes {
		input, err := decodeJSON(vector.Input)
		if err != nil {
			t.Fatal(err)
		}
		found, err := changesRequest(input)
		if err != nil {
			t.Fatalf("%s: %v", vector.Input, err)
		}
		records := make([]Value, len(found))
		for i, change := range found {
			records[i] = change.record()
		}
		output := encode(Object{"changes": records})
		if !sameJSON(t, output, vector.Output) {
			t.Errorf("%s:\n got %s\nwant %s", vector.Input, output, vector.Output)
		}
	}
	for _, input := range vectors.Invalid {
		value, err := decodeJSON(input)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := changesRequest(value); err == nil {
			t.Errorf("accepted %s", input)
		}
	}
}

func sameJSON(t *testing.T, a, b []byte) bool {
	t.Helper()
	var left, right any
	if err := json.Unmarshal(a, &left); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &right); err != nil {
		t.Fatal(err)
	}
	return reflect.DeepEqual(left, right)
}

// The package request used by the compiler's own generation tests: four aliases, one of them untyped.
func legacyRequest(t *testing.T, target string) generateRequest {
	t.Helper()
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
	const protocol = "type AuthProvider = { token(): Promise<string> };\ntype ToolClient<I, O, R> = { tools: { [K in keyof I]: (input: I[K]) => Promise<K extends keyof O ? O[K] : unknown> }; resources: { [U in keyof R]: { read(): Promise<R[U]> } }; callTool<K extends keyof I & string>(request: { name: K; arguments: I[K] }): Promise<unknown> };"
	request := `{
        "target": ` + quoted(target) + `,
        "identity": {"name": "checkout", "version": "1.0.0"},
        "requires": {"orders": {"name": "orders", "version": "1.0.0"}, "payments": {"name": "payments", "version": "2.0.0"}},
        "contracts": {"orders": ` + order + `, "with-hyphen": ` + order + `, "mysql": ` + string(nested) + `, "empty": {"tools": []}},
        "protocol": ` + quoted(protocol) + `,
        "package_fields": {"holon": "holon-generated-apps-v1"},
        "go_module": "example.com/runtime"
    }`
	value, err := decodeJSON([]byte(request))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeGenerateRequest(value)
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}

// Digests captured from the 0.0.5 native binding, before slot options existed.
func TestLegacyPackagesAreByteIdentical(t *testing.T) {
	var expected map[string]map[string]string
	data, err := os.ReadFile(filepath.Join("testdata", "generate-legacy-sha256.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &expected); err != nil {
		t.Fatal(err)
	}
	for target, want := range expected {
		files, err := generate(context.Background(), legacyRequest(t, target))
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]string{}
		for path, data := range files {
			sum := sha256.Sum256(data)
			got[path] = hex.EncodeToString(sum[:])
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("legacy %s package changed:\n got %v\nwant %v", target, got, want)
		}
	}
}

func TestRequestsAreValidated(t *testing.T) {
	for _, test := range []struct{ patch, message string }{
		{`{"target": "go", "go_module": null}`, "go_module is required"},
		{`{"runtime": {"../escape": ""}}`, "must be relative"},
		{`{"runtime": {"file": "%%"}}`, "runtime file"},
		{`{"protocol": "", "protocol_specifier": "../protocol.js"}`, "mutually exclusive"},
		{`{"runtime_specifier": ""}`, "runtime_specifier must be nonempty"},
		{`{"browser_runtime_specifier": "bad\nmodule"}`, "browser_runtime_specifier must be nonempty"},
		{`{"protocol_specifier": " "}`, "protocol_specifier must be nonempty"},
		{`{"python_module": "../escape.py"}`, "relative, normalized .py path"},
		{`{"python_module": "/absolute.py"}`, "relative, normalized .py path"},
		{`{"python_module": "holon/./apps.py"}`, "relative, normalized .py path"},
		{`{"python_module": "holon\\apps.py"}`, "relative, normalized .py path"},
		{`{"python_module": "apps.json"}`, "relative, normalized .py path"},
		{`{"python_runtime_module": ".client"}`, "absolute Python module name"},
		{`{"python_runtime_module": "holon.class"}`, "absolute Python module name"},
		{`{"banner": "marker\nimport os"}`, "banner must be nonempty"},
		{`{"contracts": {"": {"tools": []}}}`, "binding names must be nonempty"},
		{`{"unknown": true}`, "unknown field"},
		{`{"target": "wasm"}`, "unknown variant"},
	} {
		var request, patch map[string]any
		if err := json.Unmarshal([]byte(`{"target": "node", "identity": {"name": "a", "version": "1"}, "go_module": "example.com/runtime"}`), &request); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(test.patch), &patch); err != nil {
			t.Fatal(err)
		}
		for key, value := range patch {
			request[key] = value
		}
		data, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		value, err := decodeJSON(data)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := decodeGenerateRequest(value)
		if err == nil {
			_, err = generate(context.Background(), decoded)
		}
		if err == nil || !strings.Contains(err.Error(), test.message) {
			t.Errorf("%s: expected %q, got %v", test.patch, test.message, err)
		}
	}
}

func TestInvalidContractsAreRejectedBeforeGeneration(t *testing.T) {
	const tool = `{"name":"read", "inputSchema":{"type":"object"}}`
	for _, test := range []struct{ contracts, message string }{
		{`{"":{"tools":[]}}`, "binding names must be nonempty"},
		{`{"files":{"tools":[` + tool + `, ` + tool + `]}}`, "tool names must be nonempty and unique"},
		{`{"files":{"tools":[{"name":"", "inputSchema":{"type":"object"}}]}}`, "tool names must be nonempty and unique"},
		{`{"files":{"tools":[{"name":"read", "inputSchema":{"type":"string"}}]}}`, "read: inputSchema must have type object"},
		{`{"files":{"tools":[{"name":"read", "inputSchema":{"type":"object"}, "outputSchema":null}]}}`, "invalid type: null, expected a map"},
		{`{"files":{"tools":[{"name":"read", "inputSchema":{"type":"object", "properties":{"x":{"$ref":"https://example.com/schema"}}}}]}}`, "external schema reference is unsupported"},
		{`{"files":{"tools":[{"name":"read", "inputSchema":{"type":"object", "properties":{"x":{"$ref":"#/$defs/missing"}}}}]}}`, "unresolved schema reference"},
		{`{"files":{"tools":[{"name":"read", "inputSchema":{"type":"object", "properties":{"x":5}}}]}}`, "schema must be an object or boolean"},
	} {
		value, err := decodeJSON([]byte(test.contracts))
		if err != nil {
			t.Fatal(err)
		}
		contracts := map[string]Contract{}
		for alias, item := range value.(Object) {
			if contracts[alias], err = decodeContract(item); err != nil {
				break
			}
		}
		if err == nil {
			_, err = compileCatalogs(context.Background(), contracts)
		}
		if err == nil || !strings.Contains(err.Error(), test.message) {
			t.Errorf("%s: expected %q, got %v", test.contracts, test.message, err)
		}
	}
}

func TestNumbersRoundTripAsSerdeWrites(t *testing.T) {
	for literal, want := range map[string]string{
		"1": "1", "-1": "-1", "0": "0", "-0": "-0.0", "1.0": "1.0", "1.5": "1.5", "1e2": "100.0", "1E2": "100.0", "0.1": "0.1",
		"1e16": "1e+16", "1e15": "1000000000000000.0", "1234e7": "12340000000.0", "1e21": "1e+21", "1.5e-7": "1.5e-7", "0.001234": "0.001234", "0.00001234": "0.00001234", "0.000001234": "1.234e-6",
		"18446744073709551615": "18446744073709551615", "18446744073709551616": "1.8446744073709552e+19", "-9223372036854775808": "-9223372036854775808", "-9223372036854775809": "-9.223372036854776e+18",
		"3.141592653589793": "3.141592653589793", "123456789012345680000": "1.2345678901234568e+20",
	} {
		number, err := parseNumber(literal)
		if err != nil {
			t.Fatal(err)
		}
		if got := number.String(); got != want {
			t.Errorf("%s: got %s, want %s", literal, got, want)
		}
	}
	if _, err := parseNumber("1e400"); err == nil {
		t.Error("accepted an out-of-range float")
	}
}

func TestStringsAreQuotedAsSerdeWrites(t *testing.T) {
	if got, want := quoted("a\"b\\c\n\r\t\b\f\x01\x1f\x7f<>&é\u2028"), `"a\"b\\c\n\r\t\b\f\u0001\u001f`+"\x7f<>&é\u2028"+`"`; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
	if got, want := encodePretty(Object{"a": []Value{Number{kind: posInt, u: 1}, Object{}}, "b": []Value{}}), "{\n  \"a\": [\n    1,\n    {}\n  ],\n  \"b\": []\n}"; got != want {
		t.Errorf("pretty:\n%s\nwant\n%s", got, want)
	}
	if got, want := pythonDumps(Object{"a": []Value{"é😀", Number{kind: posInt, u: 1}, Number{kind: float, f: 2.5}, nil, true}, "b'": "\\\n"}), `{"a": ["\u00e9\ud83d\ude00", 1, 2.5, null, true], "b'": "\\\n"}`; got != want {
		t.Errorf("dumps: got %s, want %s", got, want)
	}
	if got := pythonRepr(`{"b'":"\\n"}`); got != `'{"b\'":"\\\\n"}'` {
		t.Errorf("repr: got %s", got)
	}
	if got := pythonRepr("it's"); got != `"it's"` {
		t.Errorf("repr: got %s", got)
	}
}
