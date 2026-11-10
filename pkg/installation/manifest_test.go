package installation

import (
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

func TestManifestRejectsInvalidDeclarations(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ name, fields, wantError string }{
		{name: "missing source", fields: "path: ''"},
		{name: "multiple sources", fields: "path: .\nremote: {url: https://mcp.example/mcp}"},
		{name: "absolute path", fields: "path: /app"},
		{name: "unknown field", fields: "unsupported: true"},
		{name: "insecure remote", fields: "remote: {url: http://mcp.example/mcp}"},
		{name: "insecure registry", fields: "registry: {name: io.github.acme/app, version: '1.0.0', registry: 'http://registry.example'}"},
		{name: "run with npm", fields: `registry: {name: io.github.acme/app, version: '1.0.0', package: {registryType: npm}}
run: {transport: {type: stdio}}`, wantError: "run requires a registry OCI package"},
		{name: "empty secret reference", fields: "arguments: {--project: {secret: ''}}"},
		{name: "literal OAuth secret", fields: "oauth: {clientID: registered, clientSecret: unsafe}"},
		{name: "missing OAuth client ID", fields: "oauth: {clientSecret: {secret: CLIENT_SECRET}}"},
		{name: "out of range port", fields: "image: ghcr.io/acme/alpha@sha256:" + strings.Repeat("a", 64) + "\nport: 65536"},
		{name: "tagged image", fields: "image: ghcr.io/acme/alpha:latest\nport: 3000"},
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
	if _, err := parseManifest([]byte(manifestFixture+"unsupported: true\n"), "fixture"); err == nil {
		t.Fatal("accepted unknown top-level field")
	}
	if _, err := parseManifest([]byte(strings.Replace(manifestFixture, "name: alpha", "name: api", 1)), "fixture"); err == nil {
		t.Fatal("accepted a reserved app name")
	}
}
