package cloud

import (
	"reflect"
	"strings"
	"testing"
)

const manifestFixture = `
apps:
  - name: alpha
    access: {server: {}}
    path: ./alpha
  - name: beta
    access: {server: {}}
    path: ./beta
`

func TestManifestRegistryURLs(t *testing.T) {
	for _, test := range []struct {
		url   string
		valid bool
	}{
		{"https://registry.example/base", true},
		{"http://localhost:1234", true},
		{"http://sub.localhost", true},
		{"http://127.0.0.1", true},
		{"http://[::1]", true},
		{"http://[::1]:1234", true},
		{"http://registry.example", false},
		{"http://[", false},
		{"https://user@registry.example", false},
		{"https://registry.example?query", false},
	} {
		t.Run(test.url, func(t *testing.T) {
			manifest := "apps:\n- name: example\n  access: {server: {}}\n" +
				"  registry: {name: io.github.acme/app, version: '1.0.0', registry: '" + test.url + "'}"
			_, err := parseManifest([]byte(manifest), "fixture")
			if (err == nil) != test.valid {
				t.Fatalf("valid=%v: %v", test.valid, err)
			}
		})
	}
}

func TestManifestRejectsInvalidDeclarations(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ name, fields, wantError string }{
		{name: "missing source", fields: "path: ''"},
		{name: "multiple sources", fields: "path: .\nremote: {url: https://mcp.example/mcp}"},
		{name: "absolute path", fields: "path: /app"},
		{name: "null source", fields: "remote: null"},
		{name: "unknown field", fields: "unsupported: true"},
		{name: "per-app origin", fields: "origin: https://browser.example"},
		{name: "empty remote", fields: "remote: {}"},
		{name: "insecure remote", fields: "remote: {url: http://mcp.example/mcp}"},
		{name: "IP remote", fields: "remote: {url: 'https://10.73.0.3/mcp'}"},
		{name: "unknown remote field", fields: "remote: {url: https://mcp.example/mcp, type: sse}"},
		{name: "unknown transport field", fields: `registry: {name: io.github.acme/app, version: '1.0.0', package: {registryType: oci}}
run: {transport: {type: stdio, headers: []}}`},
		{name: "run with npm", fields: `registry: {name: io.github.acme/app, version: '1.0.0', package: {registryType: npm}}
run: {transport: {type: stdio}}`, wantError: "run requires a registry OCI package"},
		{name: "run with PyPI", fields: `registry: {name: io.github.acme/app, version: '1.0.0', package: {registryType: pypi}}
run: {transport: {type: stdio}}`, wantError: "run requires a registry OCI package"},
		{name: "null argument", fields: "arguments: {--project: null}"},
		{name: "empty secret reference", fields: "arguments: {--project: {secret: ''}}"},
		{name: "argument list", fields: "arguments: {--project: [demo]}"},
		{name: "null egress", fields: "egress: null"},
		{name: "egress port field", fields: "egress: [{host: api.example.com, port: 443}]"},
		{name: "literal OAuth secret", fields: "oauth: {clientID: registered, clientSecret: unsafe}"},
		{name: "missing OAuth client ID", fields: "oauth: {clientSecret: {secret: CLIENT_SECRET}}"},
		{name: "secret OAuth client ID", fields: "oauth: {clientID: {secret: CLIENT_ID}}"},
		{name: "app OAuth issuer", fields: "oauth: {clientID: registered, issuer: https://issuer.example}"},
		{name: "zero port", fields: "image: ghcr.io/acme/alpha@sha256:" + strings.Repeat("a", 64) + "\nport: 0"},
		{name: "out of range port", fields: "image: ghcr.io/acme/alpha@sha256:" + strings.Repeat("a", 64) + "\nport: 65536"},
		{name: "tagged image", fields: "image: ghcr.io/acme/alpha:latest\nport: 3000"},
		{name: "image without registry", fields: "image: acme/alpha@sha256:" + strings.Repeat("a", 64) + "\nport: 3000"},
		{name: "invalid digest", fields: "image: ghcr.io/acme/alpha@sha256:bad\nport: 3000"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fields := test.fields
			if !strings.Contains(fields, "path:") && !strings.Contains(fields, "image:") &&
				!strings.Contains(fields, "registry:") && !strings.Contains(fields, "remote:") {
				fields += "\npath: ."
			}
			manifest := "apps:\n  - name: alpha\n    access: {server: {}}\n    " +
				strings.ReplaceAll(fields, "\n", "\n    ")
			if _, err := parseManifest([]byte(manifest), "fixture"); err == nil || test.wantError != "" && !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("invalid declaration: wanted %q, got %v", test.wantError, err)
			}
		})
	}
	for _, fields := range []string{
		"unsupported: true", "origins: {https://browser.example: {apps: []}}",
		"installation: {origin: https://cloud.example}",
	} {
		if _, err := parseManifest([]byte(manifestFixture+"\n"+fields+"\n"), "fixture"); err == nil {
			t.Fatalf("accepted invalid manifest: %s", fields)
		}
	}

	for _, name := range []string{"/", "a/b", "UPPER", "api", "mcp", "v2", "sessions", "default", strings.Repeat("x", 50)} {
		t.Run("name/"+name, func(t *testing.T) {
			manifest := strings.Replace(manifestFixture, "name: alpha", "name: '"+name+"'", 1)
			if _, err := parseManifest([]byte(manifest), "fixture"); err == nil {
				t.Fatal("accepted invalid or reserved name")
			}
		})
	}
}

func TestManifestCallerPolicy(t *testing.T) {
	for _, test := range []struct {
		name, access string
		valid        bool
		callers      []string
	}{
		{"omitted", `{server: {}}`, true, nil},
		{"empty", `{server: {}, callers: []}`, true, []string{}},
		{"allowlist", `{server: {oauth: {}}, callers: [beta]}`, true, []string{"beta"}},
		{"null", `{server: {}, callers: null}`, false, nil},
		{"duplicate", `{server: {}, callers: [beta, beta]}`, false, nil},
		{"invalid name", `{server: {}, callers: ['../beta']}`, false, nil},
		{"reserved name", `{server: {}, callers: [api]}`, false, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			manifest := strings.Replace(manifestFixture, "{server: {}}", test.access, 1)
			project, err := parseManifest([]byte(manifest), "fixture")
			if (err == nil) != test.valid {
				t.Fatalf("parse: %v", err)
			}
			if test.valid && !reflect.DeepEqual(project.Apps[0].Access.Callers, test.callers) {
				t.Fatalf("parsed callers = %#v, want %#v", project.Apps[0].Access.Callers, test.callers)
			}
		})
	}
}
