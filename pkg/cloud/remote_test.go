package cloud

import (
	"encoding/json"
	"testing"

	"github.com/holon-sh/cli/pkg/mcpregistry"
)

func TestRegistryDeploymentSelection(t *testing.T) {
	oci := json.RawMessage(`{"registryType":"oci","identifier":"ghcr.io/acme/example:1.0.0","transport":{"type":"stdio"}}`)
	httpOCI := json.RawMessage(`{"registryType":"oci","identifier":"ghcr.io/acme/example:1.0.0",
"transport":{"type":"streamable-http","url":"http://127.0.0.1:8082/mcp"}}`)
	invalidHTTP := json.RawMessage(`{"registryType":"oci","identifier":"ghcr.io/acme/example:1.0.0",
"transport":{"type":"streamable-http","url":"http://localhost:8082/mcp?tenant=west"}}`)
	invalidOCI := json.RawMessage(`{"registryType":"oci","identifier":"ghcr.io/acme/example:1.0.0","transport":{"type":"sse"}}`)
	secondOCI := json.RawMessage(`{"registryType":"oci","identifier":"ghcr.io/acme/second:1.0.0","transport":{"type":"stdio"}}`)
	remote := serverTransport{Type: "streamable-http", URL: "https://mcp.example/mcp"}
	for _, test := range []struct {
		name, want, image string
		packages          []json.RawMessage
		remotes           []serverTransport
		selector          registrySource
		run               *packageRun
	}{
		{name: "ambiguous", packages: []json.RawMessage{oci}, remotes: []serverTransport{remote}},
		{
			name: "OCI", packages: []json.RawMessage{oci}, remotes: []serverTransport{remote},
			selector: registrySource{Package: &packageSelection{RegistryType: "oci"}}, want: "stdio",
		},
		{name: "remote", packages: []json.RawMessage{oci}, remotes: []serverTransport{remote}, selector: registrySource{Remote: &struct{}{}}, want: "remote"},
		{
			name: "both selectors", packages: []json.RawMessage{oci}, remotes: []serverTransport{remote},
			selector: registrySource{Package: &packageSelection{RegistryType: "oci"}, Remote: &struct{}{}},
		},
		{name: "npm", packages: []json.RawMessage{oci}, selector: registrySource{Package: &packageSelection{RegistryType: "npm"}}},
		{
			name: "no package fallback", packages: []json.RawMessage{invalidOCI}, remotes: []serverTransport{remote},
			selector: registrySource{Package: &packageSelection{RegistryType: "oci"}},
		},
		{name: "infer OCI", packages: []json.RawMessage{oci}, want: "stdio"},
		{name: "infer HTTP OCI", packages: []json.RawMessage{httpOCI}, want: "http"},
		{name: "HTTP endpoint Holon cannot reach", packages: []json.RawMessage{invalidHTTP}},
		{name: "ambiguous transports", packages: []json.RawMessage{oci, httpOCI}},
		{name: "invalid HTTP package is still a candidate", packages: []json.RawMessage{invalidHTTP, oci}},
		{name: "infer remote", remotes: []serverTransport{remote}, want: "remote"},
		{
			name: "published identifier with replacement run", packages: []json.RawMessage{oci, secondOCI},
			selector: registrySource{Package: &packageSelection{RegistryType: "oci", Identifier: "ghcr.io/acme/second:1.0.0"}},
			run:      &packageRun{Transport: manifestTransport{Type: "stdio"}}, want: "stdio", image: "ghcr.io/acme/second:1.0.0",
		},
		{
			name: "invalid run environment", packages: []json.RawMessage{oci},
			selector: registrySource{Package: &packageSelection{RegistryType: "oci"}},
			run: &packageRun{Transport: manifestTransport{Type: "stdio"}, Env: map[string]mcpregistry.Value{
				"INVALID-NAME": {Value: new("value")},
			}},
		},
		{
			name: "mismatched run secret", packages: []json.RawMessage{oci},
			selector: registrySource{Package: &packageSelection{RegistryType: "oci"}},
			run: &packageRun{Transport: manifestTransport{Type: "stdio"}, Env: map[string]mcpregistry.Value{
				"TOKEN": {Secret: "OTHER_TOKEN"},
			}},
		},
		{
			name: "invalid run argument", packages: []json.RawMessage{oci},
			selector: registrySource{Package: &packageSelection{RegistryType: "oci"}},
			run:      &packageRun{Transport: manifestTransport{Type: "stdio"}, Args: []string{"line\nfeed"}},
		},
		{
			name: "invalid run transport", packages: []json.RawMessage{oci},
			selector: registrySource{Package: &packageSelection{RegistryType: "oci"}},
			run:      &packageRun{Transport: manifestTransport{Type: "sse", URL: "http://localhost:3000/mcp"}},
		},
		{name: "ambiguous remotes", remotes: []serverTransport{remote, remote}, selector: registrySource{Remote: &struct{}{}}},
		{name: "remote stdio", remotes: []serverTransport{{Type: "stdio", URL: remote.URL}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			document := descriptorDocument("example")
			document["packages"], document["remotes"] = test.packages, test.remotes
			source := test.selector
			source.RegistryRef = descriptorRegistry(t, document).RegistryRef
			manifest, err := resolveManifestGraph(t.Context(), manifestSpec{Apps: []manifestApp{{Name: "example", Registry: &source, Run: test.run}}})
			if test.want == "" {
				if err == nil {
					t.Fatal("unsupported deployment accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			app := manifest.Apps[0]
			image := test.image
			if image == "" {
				image = "ghcr.io/acme/example:1.0.0"
			}
			port := int32(3000)
			switch test.want {
			case "http":
				port = 8082
			case "remote":
				port = 0
			}
			if test.want == "remote" && (app.resolved.Remote == nil || app.resolved.Remote.URL != remote.URL || app.resolved.Image != "") ||
				test.want != "remote" && (app.resolved.Remote != nil || app.resolved.Image != image) ||
				app.resolved.Port != port {
				t.Fatalf("wrong deployment: %#v", app)
			}
		})
	}
}

func TestRemoteRejectsContainerSettings(t *testing.T) {
	document := descriptorDocument("example")
	delete(document, "packages")
	document["remotes"] = []any{map[string]any{"type": "streamable-http", "url": "https://mcp.example/mcp"}}
	ref, _ := json.Marshal(descriptorRegistry(t, document))
	for name, source := range map[string]string{"direct": "remote: {url: https://mcp.example/mcp}", "registry": "registry: " + string(ref)} {
		for _, fields := range []string{"port: 3000", "run: {}", "arguments: {}", "egress: []"} {
			t.Run(name+"/"+fields, func(t *testing.T) {
				manifest := "apps:\n- name: example\n  access: {server: {}}\n  " + source + "\n  " + fields
				parsed, err := parseManifest([]byte(manifest), "fixture")
				if err == nil {
					_, err = resolveManifestGraph(t.Context(), parsed)
				}
				if err == nil {
					t.Fatal("remote accepted container settings")
				}
			})
		}
	}
}

func TestRemoteHeaderInputs(t *testing.T) {
	for _, test := range []struct {
		name    string
		values  map[string]mcpregistry.Value
		invalid bool
		inline  bool
	}{
		{name: "inline secret literal", inline: true, values: map[string]mcpregistry.Value{"Authorization": {Value: new("secret")}}, invalid: true},
		{name: "defaults", values: map[string]mcpregistry.Value{"Authorization": {Secret: "TOKEN"}}},
		{name: "explicit empty", values: map[string]mcpregistry.Value{"Authorization": {Secret: "TOKEN"}, "X-Workspace": {Value: new("")}}},
		{name: "shared secret", values: map[string]mcpregistry.Value{"Authorization": {Secret: "TOKEN"}, "X-Key": {Secret: "TOKEN"}}},
		{name: "required missing", invalid: true},
		{name: "unknown", values: map[string]mcpregistry.Value{"Authorization": {Secret: "TOKEN"}, "Unknown": {Value: new("x")}}, invalid: true},
		{name: "secret literal", values: map[string]mcpregistry.Value{"Authorization": {Value: new("secret")}}, invalid: true},
		{name: "fixed override", values: map[string]mcpregistry.Value{"Authorization": {Secret: "TOKEN"}, "X-Fixed": {Value: new("override")}}, invalid: true},
		{name: "duplicate binding", values: map[string]mcpregistry.Value{"Authorization": {Secret: "TOKEN"}, "authorization": {Secret: "TOKEN"}}, invalid: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			document := descriptorDocument("example")
			delete(document, "packages")
			headers := []any{
				map[string]any{"name": "X-Workspace", "default": "default"},
				map[string]any{"name": "Authorization", "isSecret": true, "isRequired": true},
				map[string]any{"name": "X-Key", "isSecret": true},
				map[string]any{"name": "X-Optional"},
				map[string]any{"name": "X-Fixed", "value": "publisher"},
			}
			document["remotes"] = []any{map[string]any{"type": "streamable-http", "url": "https://mcp.example:8443/api/mcp", "headers": headers}}
			app := manifestApp{Name: "example", Registry: descriptorRegistry(t, document), Headers: test.values}
			if test.inline {
				app.Registry, app.Remote = nil, &manifestRemote{URL: "https://mcp.example/mcp"}
			}
			manifest, err := resolveManifestGraph(t.Context(), manifestSpec{Apps: []manifestApp{app}})
			if test.invalid {
				if err == nil {
					t.Fatal("invalid header configuration accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			app = manifest.Apps[0]
			if app.resolved.Remote.Headers["Authorization"] != "{TOKEN}" || !app.resolved.Env["TOKEN"].IsRequired || !app.resolved.Env["TOKEN"].IsSecret {
				t.Fatal("secret classification lost")
			}
			if _, ok := app.resolved.Remote.Headers["X-Optional"]; ok {
				t.Fatal("absent optional header injected")
			}
			workspace := "default"
			if test.name == "explicit empty" {
				workspace = ""
			}
			for header, want := range map[string]string{"X-Workspace": workspace, "X-Fixed": "publisher"} {
				if value, ok := app.resolved.Remote.Headers[header]; !ok || value != want {
					t.Fatalf("header %s lost its value", header)
				}
			}
		})
	}
}
