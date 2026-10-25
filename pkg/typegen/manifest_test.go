package typegen

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestManifestPreservesPlatformFields(t *testing.T) {
	data := `apps:
- name: local
  path: ./apps/local
  mount: /local
  port: 8000
  access: {server: {oauth: {}}, callers: [worker]}
  oauth: {clientID: registered, clientSecret: {secret: CLIENT_SECRET}}
  headers: {Authorization: {secret: TOKEN}}
  egress: [api.example.com]
  run: {transport: {type: stdio}, command: python, args: [server.py]}
  arguments: {--project: demo}
- name: worker
  image: ghcr.io/example/worker:1
- name: catalog
  registry: {name: example.com/catalog, version: "1.0.0", package: {registryType: oci}, remote: {type: streamable-http}}
- name: remote
  remote: {url: https://mcp.example.com/mcp}
`
	m, err := ParseManifest([]byte(data), "holon.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Apps) != 4 || m.Apps[0].Path != "./apps/local" {
		t.Fatalf("lost source declarations: %+v", m.Apps)
	}
	for name, field := range map[string]json.RawMessage{"access": m.Apps[0].Access, "oauth": m.Apps[0].OAuth, "headers": m.Apps[0].Headers, "egress": m.Apps[0].Egress, "run": m.Apps[0].Run, "arguments": m.Apps[0].Arguments} {
		if !json.Valid(field) {
			t.Errorf("%s was discarded: %s", name, field)
		}
	}
}

func TestManifestRejectsAmbiguousOrUnknownDeclarations(t *testing.T) {
	for _, fields := range []string{"path: app\n  unsupported: true", "path: app\n  image: image:1", "mount: /only", "path: /absolute", "registry: {name: example.com/app, version: latest}", "remote: {url: https://example.com, unknown: true}"} {
		t.Run(fields, func(t *testing.T) {
			if _, err := ParseManifest([]byte("apps:\n- name: app\n  "+fields), "holon.yaml"); err == nil {
				t.Fatal("accepted invalid declaration")
			}
		})
	}
	for _, data := range []string{"apps: []", "apps: [{name: Upper, path: app}]", "apps: [{name: app, path: app}, {name: app, image: image:1}]", "apps: [{name: app, path: app}]\nunknown: true", "apps: [{name: app, path: app}]\napps: []"} {
		if _, err := ParseManifest([]byte(data), "holon.yaml"); err == nil {
			t.Errorf("accepted %s", data)
		}
	}
}

func TestDescriptorTypesIsProviderMetadata(t *testing.T) {
	for _, location := range []string{"https://types.example.com/app.json", "http://types.example.com/app.json", "https://user:pass@types.example.com/app.json", "https://types.example.com/app.json#fragment"} {
		data := `{"name":"example.com/app","version":"1.0.0","description":"App","_meta":{"io.modelcontextprotocol.registry/publisher-provided":{"sh.holon":{"types":` + string(mustJSON(t, location)) + `}}}}`
		d, err := ParseDescriptor([]byte(data))
		valid := location == "https://types.example.com/app.json"
		if valid && (err != nil || d.Types != location) {
			t.Fatalf("valid HTTPS document rejected: %v", err)
		}
		if !valid && (err == nil || !strings.Contains(err.Error(), "HTTPS")) {
			t.Errorf("accepted invalid types URL %q: %v", location, err)
		}
	}
}
