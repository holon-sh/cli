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
	for _, test := range []struct{ name, fields string }{
		{name: "missing source", fields: "path: ''"},
		{name: "absolute path", fields: "path: /app"},
		{name: "insecure registry", fields: "registry: {name: io.github.acme/app, version: '1.0.0', registry: 'http://registry.example'}"},
		{name: "run with npm", fields: `registry: {name: io.github.acme/app, version: '1.0.0', package: {registryType: npm}}
run: {transport: {type: stdio}}`},
		{name: "tagged image", fields: "image: ghcr.io/acme/alpha:latest\nport: 3000"},
		{name: "duplicate webhook", fields: "path: ./alpha\nwebhooks: [{name: github, tool: a}, {name: github, tool: b}]"},
	} {
		t.Run(test.name, func(t *testing.T) {
			manifest := "apps:\n  - name: alpha\n    access: {server: {}}\n    " +
				strings.ReplaceAll(test.fields, "\n", "\n    ")
			if _, err := parseManifest([]byte(manifest), "fixture"); err == nil {
				t.Fatal("accepted an invalid declaration")
			}
		})
	}
	if _, err := parseManifest([]byte(strings.Replace(manifestFixture, "name: alpha", "name: api", 1)), "fixture"); err == nil {
		t.Fatal("accepted a reserved app name")
	}
}
