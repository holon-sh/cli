package installation

import (
	"bytes"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestServerJSONSchema(t *testing.T) {
	const descriptor = `{
  "name":"io.github.acme/app","version":"1.0.0","packages":[],
  "_meta":{"other.publisher":{"custom":true},"io.modelcontextprotocol.registry/publisher-provided":{
   "other.extension":{},"sh.holon":{
    "requires":{"orders":{"name":"io.github.acme/orders","version":"1.0.0"}},
    "egress":[{"host":"{API_HOST}","ports":[443]}]
   }
  }}
 }`
	generated, err := ServerJSONSchema()
	if err != nil {
		t.Fatal(err)
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(generated))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("server", document); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile("server")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, document string
		valid          bool
	}{
		{"standard fields and Holon metadata", descriptor, true},
		{"null dependencies", strings.Replace(descriptor, `"requires":{"orders":{"name":"io.github.acme/orders","version":"1.0.0"}}`, `"requires":null`, 1), true},
		{"null egress", strings.Replace(descriptor, `"egress":[{"host":"{API_HOST}","ports":[443]}]`, `"egress":null`, 1), false},
		{"unknown Holon field", strings.Replace(descriptor, `"sh.holon":{`, `"sh.holon":{"unknown":true,`, 1), false},
		{"missing dependency version", strings.Replace(descriptor,
			`"orders":{"name":"io.github.acme/orders","version":"1.0.0"}`, `"orders":{"name":"io.github.acme/orders"}`, 1), false},
		{"project app", strings.Replace(descriptor, `"orders":{"name":"io.github.acme/orders","version":"1.0.0"}`, `"orders":{}`, 1), true},
		{"dependency url", strings.Replace(descriptor,
			`"orders":{"name":"io.github.acme/orders","version":"1.0.0"}`, `"orders":{"url":"https://orders.example/mcp"}`, 1), true},
	} {
		t.Run(test.name, func(t *testing.T) {
			value, err := jsonschema.UnmarshalJSON(strings.NewReader(test.document))
			if err != nil {
				t.Fatal(err)
			}
			if err := schema.Validate(value); (err == nil) != test.valid {
				t.Fatalf("schema valid=%t: %v", test.valid, err)
			}
		})
	}
}
