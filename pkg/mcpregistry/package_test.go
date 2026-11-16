package mcpregistry

import (
	"cmp"
	"encoding/json"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"

	"github.com/holon-sh/protocol"
)

func TestPackageTemplates(t *testing.T) {
	var pkg Package
	err := json.Unmarshal([]byte(`{"registryType":"oci","transport":{"type":"streamable-http","url":"http://{HOST}:{--port}/mcp"},
		"environmentVariables":[
			{"name":"HOST","default":"0.0.0.0"},
			{"name":"API_URL","value":"https://{tenant}.example.com","isRequired":true,"variables":{"tenant":{}}}
		],
		"runtimeArguments":[{"type":"named","name":"-e","value":"TOKEN={token}","variables":{"token":{"isSecret":true}}}],
		"packageArguments":[
			{"type":"named","name":"--port","default":"9001"},
			{"type":"positional","value":"https://ecs.{region}.api.aws/mcp","variables":{"region":{"default":"us-east-1"}}}
		]}`), &pkg)
	if err != nil {
		t.Fatal(err)
	}
	if port, err := pkg.Port(); err != nil || port != 9001 {
		t.Fatalf("port = %d, %v", port, err)
	}
	app, err := pkg.App(nil, RunConfig{})
	env := app.Env
	if err != nil || !env["tenant"].IsRequired || !env["token"].IsSecret || *env["region"].Default != "us-east-1" || *env["PORT"].Default != "9001" {
		t.Fatalf("variables = %v, %v", env, err)
	}
	if !reflect.DeepEqual(app.EnvTemplates, map[string]protocol.Template{"API_URL": "https://{tenant}.example.com", "TOKEN": "{token}"}) ||
		!slices.Equal(app.Args, []protocol.Template{"--port={PORT}", "https://ecs.{region}.api.aws/mcp"}) {
		t.Fatalf("templates = %v, args = %q", app.EnvTemplates, app.Args)
	}
	pkg.Transport.URL = "http://localhost:{PORT}/mcp"
	if _, err := pkg.Port(); err == nil {
		t.Fatal("port without a published value accepted")
	}
}

func descriptorPackage(t *testing.T, environment, arguments, runtime string) Package {
	t.Helper()
	var pkg Package
	if err := json.Unmarshal([]byte(`{"registryType":"oci","identifier":"example","transport":{"type":"stdio"},
		"environmentVariables":`+cmp.Or(environment, "[]")+`,"packageArguments":`+cmp.Or(arguments, "[]")+`,"runtimeArguments":`+cmp.Or(runtime, "[]")+`}`), &pkg); err != nil {
		t.Fatal(err)
	}
	return pkg
}

func TestPackageInputs(t *testing.T) {
	pkg := descriptorPackage(t, `[{"name":"TOKEN","isSecret":true},{"name":"REGION","isRequired":true,"choices":["us","eu"]},
		{"name":"LOG_LEVEL","default":"info"},{"name":"KEY_FILE","format":"filepath"},{"name":"MODE","value":"stdio"},{"name":"EMPTY","default":""}]`,
		`[{"type":"named","name":"--mode","value":"read-only"},{"type":"named","name":"--project","isRequired":true},
		{"type":"named","name":"--api-token","isRequired":true,"isSecret":true},{"type":"named","name":"--zone","isRepeated":true},
		{"type":"named","name":"--level","isRequired":true,"default":"info"},{"type":"named","name":"--unused"},{"type":"named","name":"-p"},
		{"type":"positional","valueHint":"repoRoot"},{"type":"positional","valueHint":"suffix","default":""}]`,
		`[{"type":"named","name":"-v","value":"{dir}:/data","variables":{"dir":{}}},{"type":"named","name":"-e","value":"REGION"},
		{"type":"named","name":"-e","value":"RUNTIME_CONFIG={\"mode\":\"readonly\"}"}]`)
	wantArgs := []protocol.Template{
		"--mode=read-only", "--project={PROJECT}", "--api-token={API_TOKEN}", "--zone={ZONE}", "--level={LEVEL}", "--unused={UNUSED}",
		"-p", "{P}", "{REPO_ROOT}", "{SUFFIX}",
	}
	for _, test := range []struct {
		values map[string]Value
		inputs map[string]protocol.Input
	}{
		{
			values: map[string]Value{"--project": {Value: new("two words; $(no-shell)")}, "--level": {Value: new("debug")}, "--api-token": {Secret: "API_TOKEN"}, "suffix": {Value: new("")}},
			inputs: map[string]protocol.Input{"PROJECT": {Value: new("two words; $(no-shell)")}, "LEVEL": {Value: new("debug")}, "SUFFIX": {Value: new("")}},
		},
		{
			values: map[string]Value{"--project": {Value: new("another-manifest")}, "--api-token": {Secret: "API_TOKEN"}},
			inputs: map[string]protocol.Input{"PROJECT": {Value: new("another-manifest")}, "LEVEL": {IsRequired: true, Default: new("info")}, "SUFFIX": {Default: new("")}},
		},
	} {
		want := map[string]protocol.Input{
			"TOKEN": {IsSecret: true}, "REGION": {IsRequired: true}, "LOG_LEVEL": {Default: new("info")}, "KEY_FILE": {},
			"MODE": {Value: new("stdio")}, "EMPTY": {Default: new("")}, "RUNTIME_CONFIG": {Value: new(`{"mode":"readonly"}`)},
			"API_TOKEN": {IsRequired: true, IsSecret: true}, "ZONE": {}, "UNUSED": {}, "P": {IsRequired: true}, "REPO_ROOT": {IsRequired: true},
		}
		maps.Copy(want, test.inputs)
		app, err := pkg.App(test.values, RunConfig{})
		if err != nil {
			t.Fatal(err)
		}
		if diff := gocmp.Diff(wantArgs, app.Args); diff != "" {
			t.Fatalf("args (-want +got):\n%s", diff)
		}
		if diff := gocmp.Diff(want, app.Env); diff != "" {
			t.Fatalf("inputs (-want +got):\n%s", diff)
		}
	}
}

func TestPackageInputRejections(t *testing.T) {
	for _, test := range []struct {
		name, environment, arguments, runtime, want string
		values                                      map[string]Value
	}{
		{name: "variable name", environment: `[{"name":"URL","value":"{region-name}","variables":{"region-name":{}}}]`, want: "invalid environment name"},
		{name: "secret literal", environment: `[{"name":"TOKEN","isSecret":true,"value":"do-not-print"}]`, want: "secret store"},
		{name: "invalid text", runtime: `[{"type":"named","name":"-e","value":"TOKEN=do-not-print\u0000"}]`, want: "without NUL"},
		{
			name: "fixed value", arguments: `[{"type":"named","name":"--mode","value":"fixed"}]`,
			values: map[string]Value{"--mode": {Value: new("MODE")}}, want: "unknown argument",
		},
		{name: "undeclared placeholder", arguments: `[{"type":"positional","value":"{UNDECLARED}"}]`, want: "declared inputs"},
		{name: "positional without hint", arguments: `[{"type":"positional","default":"unresolved"}]`, want: "requires valueHint"},
		{
			name: "secret argument", arguments: `[{"type":"named","name":"--token","isSecret":true}]`,
			values: map[string]Value{"--token": {Value: new("do-not-print")}}, want: "requires a secret reference",
		},
		{
			name: "invalid argument text", arguments: `[{"type":"named","name":"--text"}]`,
			values: map[string]Value{"--text": {Value: new("do-not-print\n")}}, want: "single-line",
		},
		{
			name: "secret reference for non-secret input", arguments: `[{"type":"named","name":"--token"}]`,
			values: map[string]Value{"--token": {Secret: "TOKEN"}}, want: "isSecret",
		},
		{
			name: "argument input conflicts with environment", environment: `[{"name":"TOKEN"}]`, arguments: `[{"type":"named","name":"--token","isSecret":true}]`,
			want: "unique environment name",
		},
		{
			name: "secret reference to another input", arguments: `[{"type":"named","name":"--token","isSecret":true}]`,
			values: map[string]Value{"--token": {Secret: "OTHER"}}, want: "must name the input TOKEN",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := descriptorPackage(t, test.environment, test.arguments, test.runtime).App(test.values, RunConfig{})
			if err == nil || !strings.Contains(err.Error(), test.want) || strings.Contains(err.Error(), "do-not-print") {
				t.Fatalf("expected %s without values: %v", test.want, err)
			}
		})
	}
}
