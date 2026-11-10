package installation

import (
	"encoding/json"
	"maps"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/holon-sh/cli/pkg/mcpregistry"
	"github.com/holon-sh/protocol"
)

func TestDescriptorInputs(t *testing.T) {
	root := t.TempDir()
	document := descriptorDocument("example")
	delete(document, "$schema")
	pkg := document["packages"].([]any)[0].(map[string]any)
	pkg["transport"] = map[string]any{"type": "stdio"}
	pkg["environmentVariables"] = []any{
		map[string]any{"name": "TOKEN", "isSecret": true},
		map[string]any{"name": "REGION", "isRequired": true, "choices": []any{"us", "eu"}},
		map[string]any{"name": "LOG_LEVEL", "default": "info"},
		map[string]any{"name": "KEY_FILE", "format": "filepath"},
		map[string]any{"name": "MODE", "value": "stdio"},
		map[string]any{"name": "EMPTY", "default": ""},
	}
	pkg["runtimeArguments"] = []any{
		map[string]any{"type": "named", "name": "-v", "value": "{dir}:/data", "variables": map[string]any{"dir": map[string]any{}}},
		map[string]any{"type": "named", "name": "-e", "value": "REGION"},
		map[string]any{"type": "named", "name": "-e", "value": `RUNTIME_CONFIG={"mode":"readonly"}`},
	}
	pkg["packageArguments"] = []any{
		map[string]any{"type": "named", "name": "--mode", "value": "read-only"},
		map[string]any{"type": "named", "name": "--project", "isRequired": true},
		map[string]any{"type": "named", "name": "--api-token", "isRequired": true, "isSecret": true},
		map[string]any{"type": "named", "name": "--zone", "isRepeated": true},
		map[string]any{"type": "named", "name": "--level", "isRequired": true, "default": "info"},
		map[string]any{"type": "named", "name": "--unused"},
		map[string]any{"type": "named", "name": "-p"},
		map[string]any{"type": "positional", "valueHint": "repoRoot"},
		map[string]any{"type": "positional", "valueHint": "suffix", "default": ""},
	}
	registry := descriptorRegistry(t, document)
	descriptor, _ := json.Marshal(document)
	sourceFiles(t, root, map[string]string{
		"server.json": string(descriptor),
		manifestFileName: `apps:
  - name: example
    registry: {name: io.github.acme/example, version: 1.4.2, registry: "` + registry.Registry + `"}
    access: {server: {}}
    arguments:
      --project: 'two words; $(no-shell)'
      --level: debug
      --api-token: {secret: API_TOKEN}
      suffix: ''
  - name: other
    path: .
    access: {server: {}}
    arguments:
      --project: another-manifest
      --api-token: {secret: API_TOKEN}
`,
	})
	manifest, err := resolveManifestFile(filepath.Join(root, manifestFileName))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err = resolveManifestGraph(t.Context(), manifest)
	if err != nil {
		t.Fatal(err)
	}
	wantArgs := []protocol.Template{
		"--mode=read-only", "--project={PROJECT}", "--api-token={API_TOKEN}", "--zone={ZONE}", "--level={LEVEL}", "--unused={UNUSED}",
		"-p", "{P}", "{REPO_ROOT}", "{SUFFIX}",
	}
	for index, arguments := range []map[string]protocol.Input{
		{"PROJECT": {Value: new("two words; $(no-shell)")}, "LEVEL": {Value: new("debug")}, "SUFFIX": {Value: new("")}},
		{"PROJECT": {Value: new("another-manifest")}, "LEVEL": {IsRequired: true, Default: new("info")}, "SUFFIX": {Default: new("")}},
	} {
		app := manifest.Apps[index]
		want := map[string]protocol.Input{
			"TOKEN": {IsSecret: true}, "REGION": {IsRequired: true}, "LOG_LEVEL": {Default: new("info")}, "KEY_FILE": {},
			"MODE": {Value: new("stdio")}, "EMPTY": {Default: new("")}, "RUNTIME_CONFIG": {Value: new(`{"mode":"readonly"}`)},
			"API_TOKEN": {IsRequired: true, IsSecret: true}, "ZONE": {}, "UNUSED": {}, "P": {IsRequired: true}, "REPO_ROOT": {IsRequired: true},
		}
		maps.Copy(want, arguments)
		if diff := cmp.Diff(wantArgs, app.resolved.Args); diff != "" {
			t.Fatalf("%s args (-want +got):\n%s", app.Name, diff)
		}
		if diff := cmp.Diff(want, app.resolved.Env); diff != "" {
			t.Fatalf("%s inputs (-want +got):\n%s", app.Name, diff)
		}
	}
}

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
			_, err := resolveManifestGraph(t.Context(), manifestSpec{access: &protocol.AppAccess{Server: &protocol.AccessRule{}}, Apps: []manifestApp{{
				Name: "example", Registry: descriptorRegistry(t, document), Arguments: test.values,
			}}})
			if err == nil || !strings.Contains(err.Error(), test.want) || strings.Contains(err.Error(), "do-not-print") {
				t.Fatalf("expected %s without values: %v", test.want, err)
			}
		})
	}
}
