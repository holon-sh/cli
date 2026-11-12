package installation

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
	secondOCI := json.RawMessage(`{"registryType":"oci","identifier":"ghcr.io/acme/second:1.0.0","transport":{"type":"stdio"}}`)
	remote := mcpregistry.Transport{Type: "streamable-http", URL: "https://mcp.example/mcp"}
	type deployment struct {
		image, remote string
		port          int32
	}
	stdio := &deployment{image: "ghcr.io/acme/example:1.0.0", port: 3000}
	for _, test := range []struct {
		name     string
		packages []json.RawMessage
		remotes  []mcpregistry.Transport
		selector protocol.RegistrySource
		run      *packageRun
		want     *deployment
		rejected string
	}{
		{name: "package beside a remote", packages: []json.RawMessage{oci}, remotes: []mcpregistry.Transport{remote}, want: stdio},
		{name: "npm", packages: []json.RawMessage{oci}, selector: protocol.RegistrySource{Package: &protocol.PackageSelection{RegistryType: "npm"}}, rejected: "no matching OCI, npm or PyPI package"},
		{name: "infer HTTP OCI", packages: []json.RawMessage{httpOCI}, want: &deployment{image: "ghcr.io/acme/example:1.0.0", port: 8082}},
		{name: "HTTP endpoint Holon cannot reach", packages: []json.RawMessage{invalidHTTP}, rejected: "transport.url must be"},
		{name: "invalid HTTP package is still a candidate", packages: []json.RawMessage{invalidHTTP, oci}, rejected: "ambiguous"},
		{name: "remotes only", remotes: []mcpregistry.Transport{remote}, rejected: remote.URL},
		{
			name: "published identifier with replacement run", packages: []json.RawMessage{oci, secondOCI},
			selector: protocol.RegistrySource{Package: &protocol.PackageSelection{RegistryType: "oci", Identifier: "ghcr.io/acme/second:1.0.0"}},
			run:      &packageRun{Transport: manifestTransport{Type: "stdio"}}, want: &deployment{image: "ghcr.io/acme/second:1.0.0", port: 3000},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			document := descriptorDocument("example")
			document["packages"], document["remotes"] = test.packages, test.remotes
			source := test.selector
			source.RegistryRef = descriptorRegistry(t, document).RegistryRef
			manifest, err := resolveManifestGraph(t.Context(), manifestSpec{access: &protocol.AppAccess{Server: &protocol.AccessRule{}}, Apps: []manifestApp{{Name: "example", Registry: &source, Run: test.run}}})
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
	for _, fields := range []string{"port: 3000", "arguments: {}"} {
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
	for _, test := range []struct {
		name    string
		headers map[string]mcpregistry.Value
		want    map[string]string
	}{
		{
			name:    "literals and a shared secret",
			headers: map[string]mcpregistry.Value{"authorization": {Secret: "TOKEN"}, "X-Key": {Secret: "TOKEN"}, "X-Workspace": {Value: new("")}},
			want:    map[string]string{"Authorization": "{TOKEN}", "X-Key": "{TOKEN}", "X-Workspace": ""},
		},
		{name: "authorization literal", headers: map[string]mcpregistry.Value{"Authorization": {Value: new("secret")}}},
		{name: "duplicate header", headers: map[string]mcpregistry.Value{"Authorization": {Secret: "TOKEN"}, "authorization": {Secret: "TOKEN"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			app := manifestApp{Name: "example", Remote: &manifestRemote{URL: "https://mcp.example/mcp"}, Headers: test.headers}
			manifest, err := resolveManifestGraph(t.Context(), manifestSpec{access: &protocol.AppAccess{Server: &protocol.AccessRule{}}, Apps: []manifestApp{app}})
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
