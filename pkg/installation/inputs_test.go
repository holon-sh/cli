package installation

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/holon-sh/cli/pkg/mcpregistry"
)

func TestDescriptorInputRejections(t *testing.T) {
	for _, test := range []struct {
		name, environment, arguments, runtime, want string
		values                                      map[string]mcpregistry.Value
	}{
		{name: "variable name", environment: `[{"name":"URL","value":"{region-name}","variables":{"region-name":{}}}]`, want: "invalid environment name"},
		{name: "secret literal", environment: `[{"name":"TOKEN","isSecret":true,"value":"do-not-print"}]`, want: "secret store"},
		{name: "secret default", environment: `[{"name":"TOKEN","isSecret":true,"default":"do-not-print"}]`, want: "secret store"},
		{name: "oversized environment", environment: `[{"name":"` + strings.Repeat("X", 129) + `"}]`, want: "invalid environment name"},
		{name: "reserved environment", environment: `[{"name":"HOLON_MCP_RUNTIME_CONFIG"}]`, want: "invalid environment name"},
		{name: "ambiguous values", environment: `[{"name":"TOKEN","value":"fixed","default":"fallback"}]`, want: "both value and default"},
		{name: "invalid text", runtime: `[{"type":"named","name":"-e","value":"TOKEN=do-not-print\u0000"}]`, want: "without NUL"},
		{
			name: "fixed value", arguments: `[{"type":"named","name":"--mode","value":"fixed"}]`,
			values: map[string]mcpregistry.Value{"--mode": {Value: new("MODE")}}, want: "unknown argument",
		},
		{name: "argument without an input name", arguments: `[{"type":"named","name":"--2fa"}]`, want: "unique environment name"},
		{name: "undeclared placeholder", arguments: `[{"type":"positional","value":"{UNDECLARED}"}]`, want: "declared inputs"},
		{name: "positional without hint", arguments: `[{"type":"positional","default":"unresolved"}]`, want: "requires valueHint"},
		{
			name: "secret argument", arguments: `[{"type":"named","name":"--token","isSecret":true}]`,
			values: map[string]mcpregistry.Value{"--token": {Value: new("do-not-print")}}, want: "requires a secret reference",
		},
		{
			name: "invalid argument text", arguments: `[{"type":"named","name":"--text"}]`,
			values: map[string]mcpregistry.Value{"--text": {Value: new("do-not-print\n")}}, want: "single-line",
		},
		{
			name: "secret reference for non-secret input", arguments: `[{"type":"named","name":"--token"}]`,
			values: map[string]mcpregistry.Value{"--token": {Secret: "TOKEN"}}, want: "isSecret",
		},
		{
			name: "argument input conflicts with environment", environment: `[{"name":"TOKEN"}]`, arguments: `[{"type":"named","name":"--token","isSecret":true}]`,
			want: "unique environment name",
		},
		{
			name: "secret reference to another input", arguments: `[{"type":"named","name":"--token","isSecret":true}]`,
			values: map[string]mcpregistry.Value{"--token": {Secret: "OTHER"}}, want: "must name the input TOKEN",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			document := descriptorDocument("example")
			pkg := document["packages"].([]any)[0].(map[string]any)
			pkg["transport"] = map[string]any{"type": "stdio"}
			for key, raw := range map[string]string{"environmentVariables": test.environment, "packageArguments": test.arguments, "runtimeArguments": test.runtime} {
				if raw != "" {
					pkg[key] = json.RawMessage(raw)
				}
			}
			_, err := resolveManifestGraph(t.Context(), manifestSpec{Apps: []manifestApp{{
				Name: "example", Registry: descriptorRegistry(t, document), Arguments: test.values,
			}}})
			if err == nil || !strings.Contains(err.Error(), test.want) || strings.Contains(err.Error(), "do-not-print") {
				t.Fatalf("expected %s without values: %v", test.want, err)
			}
		})
	}
}
