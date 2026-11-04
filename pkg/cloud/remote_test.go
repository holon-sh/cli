package cloud

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/holon-sh/cli/pkg/mcpregistry"
	"github.com/holon-sh/protocol"
)

func TestRegistryDeploymentSelection(t *testing.T) {
	oci := json.RawMessage(`{"registryType":"oci","identifier":"ghcr.io/acme/example:1.0.0","transport":{"type":"stdio"}}`)
	httpOCI := json.RawMessage(`{"registryType":"oci","identifier":"ghcr.io/acme/example:1.0.0",
"transport":{"type":"streamable-http","url":"http://127.0.0.1:8082/mcp"}}`)
	invalidHTTP := json.RawMessage(`{"registryType":"oci","identifier":"ghcr.io/acme/example:1.0.0",
"transport":{"type":"streamable-http","url":"http://localhost:8082/mcp?tenant=west"}}`)
	invalidOCI := json.RawMessage(`{"registryType":"oci","identifier":"ghcr.io/acme/example:1.0.0","transport":{"type":"sse"}}`)
	secondOCI := json.RawMessage(`{"registryType":"oci","identifier":"ghcr.io/acme/second:1.0.0","transport":{"type":"stdio"}}`)
	remote := mcpregistry.Transport{Type: "streamable-http", URL: "https://mcp.example/mcp"}
	type deployment struct {
		image, remote string
		port          int32
	}
	stdio, hosted := &deployment{image: "ghcr.io/acme/example:1.0.0", port: 3000}, &deployment{remote: remote.URL}
	for _, test := range []struct {
		name     string
		packages []json.RawMessage
		remotes  []mcpregistry.Transport
		selector protocol.RegistrySource
		run      *packageRun
		want     *deployment
		rejected string
	}{
		{name: "ambiguous", packages: []json.RawMessage{oci}, remotes: []mcpregistry.Transport{remote}, rejected: "ambiguous"},
		{
			name: "OCI", packages: []json.RawMessage{oci}, remotes: []mcpregistry.Transport{remote},
			selector: protocol.RegistrySource{Package: &protocol.PackageSelection{RegistryType: "oci"}}, want: stdio,
		},
		{name: "remote", packages: []json.RawMessage{oci}, remotes: []mcpregistry.Transport{remote}, selector: protocol.RegistrySource{Remote: &struct{}{}}, want: hosted},
		{
			name: "both selectors", packages: []json.RawMessage{oci}, remotes: []mcpregistry.Transport{remote},
			selector: protocol.RegistrySource{Package: &protocol.PackageSelection{RegistryType: "oci"}, Remote: &struct{}{}},
			rejected: "mutually exclusive",
		},
		{name: "npm", packages: []json.RawMessage{oci}, selector: protocol.RegistrySource{Package: &protocol.PackageSelection{RegistryType: "npm"}}, rejected: "server requires"},
		{
			name: "no package fallback", packages: []json.RawMessage{invalidOCI}, remotes: []mcpregistry.Transport{remote},
			selector: protocol.RegistrySource{Package: &protocol.PackageSelection{RegistryType: "oci"}}, rejected: "transport.type must be",
		},
		{name: "infer OCI", packages: []json.RawMessage{oci}, want: stdio},
		{name: "infer HTTP OCI", packages: []json.RawMessage{httpOCI}, want: &deployment{image: "ghcr.io/acme/example:1.0.0", port: 8082}},
		{name: "HTTP endpoint Holon cannot reach", packages: []json.RawMessage{invalidHTTP}, rejected: "transport.url must be"},
		{name: "invalid HTTP package is still a candidate", packages: []json.RawMessage{invalidHTTP, oci}, rejected: "ambiguous"},
		{name: "infer remote", remotes: []mcpregistry.Transport{remote}, want: hosted},
		{
			name: "published identifier with replacement run", packages: []json.RawMessage{oci, secondOCI},
			selector: protocol.RegistrySource{Package: &protocol.PackageSelection{RegistryType: "oci", Identifier: "ghcr.io/acme/second:1.0.0"}},
			run:      &packageRun{Transport: manifestTransport{Type: "stdio"}}, want: &deployment{image: "ghcr.io/acme/second:1.0.0", port: 3000},
		},
		{
			name: "invalid run environment", packages: []json.RawMessage{oci},
			selector: protocol.RegistrySource{Package: &protocol.PackageSelection{RegistryType: "oci"}},
			run: &packageRun{Transport: manifestTransport{Type: "stdio"}, Env: map[string]mcpregistry.Value{
				"INVALID-NAME": {Value: new("value")},
			}},
			rejected: "invalid environment name",
		},
		{
			name: "mismatched run secret", packages: []json.RawMessage{oci},
			selector: protocol.RegistrySource{Package: &protocol.PackageSelection{RegistryType: "oci"}},
			run: &packageRun{Transport: manifestTransport{Type: "stdio"}, Env: map[string]mcpregistry.Value{
				"TOKEN": {Secret: "OTHER_TOKEN"},
			}},
			rejected: "secret key must match",
		},
		{
			name: "invalid run argument", packages: []json.RawMessage{oci},
			selector: protocol.RegistrySource{Package: &protocol.PackageSelection{RegistryType: "oci"}},
			run:      &packageRun{Transport: manifestTransport{Type: "stdio"}, Args: []string{"line\nfeed"}}, rejected: "single-line",
		},
		{
			name: "invalid run transport", packages: []json.RawMessage{oci},
			selector: protocol.RegistrySource{Package: &protocol.PackageSelection{RegistryType: "oci"}},
			run:      &packageRun{Transport: manifestTransport{Type: "sse", URL: "http://localhost:3000/mcp"}}, rejected: "transport.type must be",
		},
		{name: "ambiguous remotes", remotes: []mcpregistry.Transport{remote, remote}, selector: protocol.RegistrySource{Remote: &struct{}{}}, rejected: "ambiguous"},
		{name: "remote stdio", remotes: []mcpregistry.Transport{{Type: "stdio", URL: remote.URL}}, rejected: "server requires"},
	} {
		t.Run(test.name, func(t *testing.T) {
			document := descriptorDocument("example")
			document["packages"], document["remotes"] = test.packages, test.remotes
			source := test.selector
			source.RegistryRef = descriptorRegistry(t, document).RegistryRef
			manifest, err := resolveManifestGraph(t.Context(), manifestSpec{Apps: []manifestApp{{Name: "example", Registry: &source, Run: test.run}}})
			if test.want == nil {
				if err == nil || !strings.Contains(err.Error(), test.rejected) {
					t.Fatalf("want %q, got %v", test.rejected, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			app := manifest.Apps[0].resolved
			got := deployment{image: app.Image, port: app.Port}
			if app.Remote != nil {
				got.remote = app.Remote.URL
			}
			if got != *test.want {
				t.Fatalf("deployment = %+v, want %+v", got, *test.want)
			}
		})
	}
}

func TestRemoteRejectsContainerSettings(t *testing.T) {
	for _, fields := range []string{"port: 3000", "arguments: {}", "egress: []"} {
		t.Run(fields, func(t *testing.T) {
			manifest := "apps:\n- name: example\n  access: {server: {}}\n  remote: {url: https://mcp.example/mcp}\n  " + fields
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

func TestRemoteHeaderInputs(t *testing.T) {
	document := descriptorDocument("example")
	delete(document, "packages")
	document["remotes"] = []any{map[string]any{"type": "streamable-http", "url": "https://mcp.example:8443/api/mcp", "headers": []any{
		map[string]any{"name": "X-Workspace", "default": "default"},
		map[string]any{"name": "Authorization", "isSecret": true, "isRequired": true},
		map[string]any{"name": "X-Key", "isSecret": true},
		map[string]any{"name": "X-Optional"},
		map[string]any{"name": "X-Fixed", "value": "publisher"},
	}}}
	registry := descriptorRegistry(t, document)
	for _, test := range []struct {
		name   string
		values map[string]mcpregistry.Value
		inline bool
		want   map[string]string
	}{
		{name: "inline secret literal", inline: true, values: map[string]mcpregistry.Value{"Authorization": {Value: new("secret")}}},
		{
			name: "defaults", values: map[string]mcpregistry.Value{"Authorization": {Secret: "TOKEN"}},
			want: map[string]string{"Authorization": "{TOKEN}", "X-Workspace": "default", "X-Fixed": "publisher"},
		},
		{
			name:   "explicit empty and shared secret",
			values: map[string]mcpregistry.Value{"Authorization": {Secret: "TOKEN"}, "X-Key": {Secret: "TOKEN"}, "X-Workspace": {Value: new("")}},
			want:   map[string]string{"Authorization": "{TOKEN}", "X-Key": "{TOKEN}", "X-Workspace": "", "X-Fixed": "publisher"},
		},
		{name: "required missing"},
		{name: "unknown", values: map[string]mcpregistry.Value{"Authorization": {Secret: "TOKEN"}, "Unknown": {Value: new("x")}}},
		{name: "fixed override", values: map[string]mcpregistry.Value{"Authorization": {Secret: "TOKEN"}, "X-Fixed": {Value: new("override")}}},
		{name: "duplicate binding", values: map[string]mcpregistry.Value{"Authorization": {Secret: "TOKEN"}, "authorization": {Secret: "TOKEN"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			app := manifestApp{Name: "example", Registry: registry, Headers: test.values}
			if test.inline {
				app.Registry, app.Remote = nil, &manifestRemote{URL: "https://mcp.example/mcp"}
			}
			manifest, err := resolveManifestGraph(t.Context(), manifestSpec{Apps: []manifestApp{app}})
			if test.want == nil {
				if err == nil {
					t.Fatal("invalid header configuration accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			resolved := manifest.Apps[0].resolved
			if diff := cmp.Diff(test.want, resolved.Remote.Headers); diff != "" || !resolved.Env["TOKEN"].IsRequired || !resolved.Env["TOKEN"].IsSecret {
				t.Fatalf("headers (-want +got):\n%s\ninputs: %v", diff, resolved.Env)
			}
		})
	}
}
