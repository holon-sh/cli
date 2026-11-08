package installation

import (
	"bytes"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestConfigurationJSONSchemas(t *testing.T) {
	const apps = `[
   {"name":"alpha","path":"./local","mount":"/","access":{"server":{}},
    "oauth":{"clientID":"registered","clientSecret":{"secret":"TOKEN"}}},
   {"name":"beta","registry":{"name":"io.github.acme/app","version":"1.0.0","package":{"registryType":"oci"}},
    "access":{"server":{}},"run":{"transport":{"type":"stdio"},"env":{"TEXT":"hello","TOKEN":{"secret":"TOKEN"}}}},
   {"name":"gamma","remote":{"url":"https://mcp.example.com/mcp"},"access":{"server":{}},
    "oauth":{"clientID":"{CLIENT_ID}"}}
  ]`
	const manifest = `{
  "apps":` + apps + `
 }`
	const descriptor = `{
  "name":"io.github.acme/app","version":"1.0.0","packages":[],
  "_meta":{"other.publisher":{"custom":true},"io.modelcontextprotocol.registry/publisher-provided":{
   "other.extension":{},"sh.holon":{
    "requires":{"orders":{"name":"io.github.acme/orders","version":"1.0.0"}},
    "egress":[{"host":"{API_HOST}","ports":[443]}]
   }
  }}
 }`
	type schemaCase struct {
		name, document string
		valid          bool
	}
	for _, reference := range []struct {
		name     string
		generate func() ([]byte, error)
		cases    []schemaCase
	}{
		{"project", ProjectJSONSchema, []schemaCase{
			{"manifest value forms", manifest, true},
			{"HTTP run", strings.Replace(manifest, `"type":"stdio"`, `"type":"streamable-http","url":"http://localhost:8080/mcp"`, 1), true},
			{"unknown field", strings.Replace(manifest, `"apps":`, `"unknown":true,"apps":`, 1), false},
			{"unknown app field", strings.Replace(manifest, `"name":"alpha"`, `"name":"alpha","unknown":true`, 1), false},
			{"missing access", strings.Replace(manifest, `,"access":{"server":{}}`, "", 1), false},
			{"missing client ID", strings.Replace(manifest, `"clientID":"registered",`, "", 1), false},
			{"empty reference", strings.Replace(manifest, `{"secret":"TOKEN"}`, `{}`, 1), false},
		}},
		{"server", ServerJSONSchema, []schemaCase{
			{"standard fields and Holon metadata", descriptor, true},
			{"null dependencies", strings.Replace(descriptor, `"requires":{"orders":{"name":"io.github.acme/orders","version":"1.0.0"}}`, `"requires":null`, 1), true},
			{"null egress", strings.Replace(descriptor, `"egress":[{"host":"{API_HOST}","ports":[443]}]`, `"egress":null`, 1), false},
			{"unknown Holon field", strings.Replace(descriptor, `"sh.holon":{`, `"sh.holon":{"unknown":true,`, 1), false},
			{"missing dependency version", strings.Replace(descriptor,
				`"orders":{"name":"io.github.acme/orders","version":"1.0.0"}`, `"orders":{"name":"io.github.acme/orders"}`, 1), false},
			{"dependency url", strings.Replace(descriptor,
				`"orders":{"name":"io.github.acme/orders","version":"1.0.0"}`, `"orders":{"url":"https://orders.example/mcp"}`, 1), true},
		}},
	} {
		t.Run(reference.name, func(t *testing.T) {
			generated, err := reference.generate()
			if err != nil {
				t.Fatal(err)
			}
			document, err := jsonschema.UnmarshalJSON(bytes.NewReader(generated))
			if err != nil {
				t.Fatal(err)
			}
			compiler := jsonschema.NewCompiler()
			if err := compiler.AddResource(reference.name, document); err != nil {
				t.Fatal(err)
			}
			schema, err := compiler.Compile(reference.name)
			if err != nil {
				t.Fatal(err)
			}
			for _, test := range reference.cases {
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
		})
	}
}
