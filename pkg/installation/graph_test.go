package installation

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/holon-sh/cli/pkg/mcpregistry"
	"github.com/holon-sh/protocol"
)

func descriptorDocument(name string) map[string]any {
	return map[string]any{
		"$schema": "https://static.modelcontextprotocol.io/schemas/2025-12-11/server.schema.json",
		"name":    "io.github.acme/" + name, "version": "1.4.2", "description": "Example MCP server",
		"packages": []any{map[string]any{
			"registryType": "oci", "identifier": "ghcr.io/acme/" + name + "@sha256:" + strings.Repeat("a", 64),
			"transport": map[string]any{"type": "streamable-http", "url": "http://localhost:3000/mcp"},
		}},
	}
}

func descriptorRegistry(t *testing.T, document map[string]any) *protocol.RegistrySource {
	t.Helper()
	catalog := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"server": document})
	}))
	t.Cleanup(catalog.Close)
	return &protocol.RegistrySource{RegistryRef: protocol.RegistryRef{
		Name: document["name"].(string), Version: document["version"].(string), Registry: catalog.URL,
	}}
}

func writeDescriptor(t *testing.T, path string, document map[string]any) {
	t.Helper()
	data, _ := json.Marshal(document)
	sourceFiles(t, filepath.Dir(path), map[string]string{filepath.Base(path): string(data)})
}

func TestDescriptorInstallAndSourceReplacement(t *testing.T) {
	beta := descriptorDocument("beta")
	beta["packages"].([]any)[0].(map[string]any)["transport"].(map[string]any)["url"] = "http://localhost:8080/mcp"
	catalog := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.EscapedPath() != "/v0.1/servers/io.github.acme%2Fbeta/versions/1.4.2" {
			t.Errorf("unexpected catalog request %s %s", r.Method, r.URL)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"server": beta})
	}))
	defer catalog.Close()
	root := t.TempDir()
	t.Chdir(root)
	ref := protocol.RegistryRef{Name: "io.github.acme/beta", Version: "1.4.2", Registry: catalog.URL}
	alpha := descriptorDocument("alpha")
	delete(alpha, "packages")
	alpha["remotes"] = []any{map[string]any{"type": "streamable-http", "url": "https://mcp.example/mcp"}}
	alpha["_meta"] = map[string]any{mcpregistry.PublisherMetadata: map[string]any{
		"sh.holon": map[string]any{"requires": map[string]protocol.RegistryRef{"beta": ref}},
	}}
	writeDescriptor(t, filepath.Join(root, "alpha/server.json"), alpha)
	policy := &protocol.AppAccess{Server: &protocol.AccessRule{}}
	manifest := manifestSpec{Apps: []manifestApp{
		{Name: "alpha", Path: filepath.Join(root, "alpha"), Access: policy},
		{Name: "beta", Registry: &protocol.RegistrySource{RegistryRef: ref}, Access: policy},
	}}
	resolved, err := resolveManifestGraph(t.Context(), manifest)
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved.Apps) != 2 || resolved.Apps[1].resolved.Image == "" ||
		resolved.Apps[0].resolved.Port != 3000 || resolved.Apps[1].resolved.Port != 8080 {
		t.Fatalf("incorrect installation: %#v", resolved)
	}
	catalog.Close()
	delete(beta["packages"].([]any)[0].(map[string]any), "identifier")
	beta["remotes"] = []any{map[string]any{"type": "streamable-http", "url": "https://mcp.example/mcp"}}
	writeDescriptor(t, filepath.Join(root, "beta/server.json"), beta)
	manifest.Apps[1] = manifestApp{Name: "beta", Path: filepath.Join(root, "beta"), Access: policy}
	local, err := resolveManifestGraph(t.Context(), manifest)
	if err != nil {
		t.Fatal(err)
	}
	if local.Apps[1].resolved.Image != "" || local.Apps[1].resolved.Port != 8080 {
		t.Fatalf("local replacement failed: %#v", local.Apps[1])
	}
	manifest.Apps[1] = manifestApp{Name: "beta", Remote: &manifestRemote{URL: "https://beta.example/mcp"}, Access: policy}
	if remote, err := resolveManifestGraph(t.Context(), manifest); err != nil || remote.Apps[1].resolved.Remote == nil {
		t.Fatalf("a pinned dependency is not the project's remote app: %v", err)
	}
	manifest.Apps[1] = manifestApp{Name: "beta", Path: filepath.Join(root, "beta"), Access: policy}
	cycleBeta := descriptorDocument("beta")
	cycleBeta["_meta"] = map[string]any{mcpregistry.PublisherMetadata: map[string]any{
		"sh.holon": map[string]any{"requires": map[string]protocol.RegistryRef{
			"alpha": {Name: "io.github.acme/alpha", Version: "1.4.2"},
		}},
	}}
	writeDescriptor(t, filepath.Join(root, "beta-cycle/server.json"), cycleBeta)
	secretAlpha := descriptorDocument("alpha")
	secretAlpha["_meta"] = alpha["_meta"]
	secretAlpha["packages"].([]any)[0].(map[string]any)["environmentVariables"] = []any{
		map[string]any{"name": "BETA_MCP_URL", "isSecret": true},
	}
	writeDescriptor(t, filepath.Join(root, "alpha-secret/server.json"), secretAlpha)
	orphanAlpha := descriptorDocument("alpha")
	orphanAlpha["_meta"] = map[string]any{mcpregistry.PublisherMetadata: map[string]any{
		"sh.holon": map[string]any{"requires": map[string]protocol.RegistryRef{"zeta": {Name: "io.github.acme/zeta", Version: "1.0.0"}}},
	}}
	writeDescriptor(t, filepath.Join(root, "alpha-orphan/server.json"), orphanAlpha)
	writeDescriptor(t, filepath.Join(root, "invalid/server.json"), map[string]any{})
	invalidMetadata := descriptorDocument("invalid")
	invalidMetadata["_meta"] = map[string]any{mcpregistry.PublisherMetadata: map[string]any{
		"sh.holon": map[string]any{"egress": json.RawMessage("null")},
	}}
	writeDescriptor(t, filepath.Join(root, "invalid-metadata/server.json"), invalidMetadata)
	for _, test := range []struct {
		name, want string
		change     func(*manifestSpec)
	}{
		{name: "missing directory", change: func(p *manifestSpec) {
			p.Apps = []manifestApp{{Name: "example", Path: filepath.Join(root, "missing"), Access: policy}}
		}},
		{name: "file source", change: func(p *manifestSpec) {
			p.Apps = []manifestApp{{Name: "example", Path: filepath.Join(root, "beta/server.json"), Access: policy}}
		}},
		{name: "invalid descriptor", change: func(p *manifestSpec) {
			p.Apps = []manifestApp{{Name: "example", Path: filepath.Join(root, "invalid"), Access: policy}}
		}},
		{name: "invalid publisher metadata", want: "sh.holon metadata", change: func(p *manifestSpec) {
			p.Apps = []manifestApp{{Name: "example", Path: filepath.Join(root, "invalid-metadata"), Access: policy}}
		}},
		{name: "dependency directory without access", want: "app beta needs an access rule", change: func(p *manifestSpec) { p.Apps = p.Apps[:1] }},
		{name: "undeclared dependency", want: `registry: {name: io.github.acme/zeta, version: "1.0.0"}`, change: func(p *manifestSpec) {
			p.Apps[0].Path = filepath.Join(root, "alpha-orphan")
		}},
		{name: "dependency cycle", want: "cycle", change: func(p *manifestSpec) {
			p.Apps[1].Path = filepath.Join(root, "beta-cycle")
		}},
		{name: "secret dependency endpoint", want: "dependency endpoint", change: func(p *manifestSpec) {
			p.Apps[0].Path = filepath.Join(root, "alpha-secret")
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			invalid := manifest
			invalid.Apps = append([]manifestApp(nil), manifest.Apps...)
			test.change(&invalid)
			if _, err := resolveManifestGraph(t.Context(), invalid); err == nil || test.want != "" && !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected %q, got %v", test.want, err)
			}
		})
	}
}

func TestDescriptorEgress(t *testing.T) {
	appEgress := []any{map[string]any{"host": "API.Example.com.", "ports": []int{8443, 443, 443}}}
	for _, test := range []struct {
		name     string
		metadata any
		want     protocol.Egress
	}{
		{name: "app declaration", metadata: appEgress, want: protocol.Egress{{Host: "api.example.com", Ports: []int32{443, 8443}}}},
		{name: "app deny all", metadata: []any{}, want: protocol.Egress{}},
		{name: "no declaration"},
	} {
		t.Run(test.name, func(t *testing.T) {
			document := descriptorDocument("alpha")
			if test.metadata != nil {
				document["_meta"] = map[string]any{mcpregistry.PublisherMetadata: map[string]any{
					"sh.holon": map[string]any{"egress": test.metadata},
				}}
			}
			path := filepath.Join(t.TempDir(), "server.json")
			writeDescriptor(t, path, document)
			sourceFiles(t, filepath.Dir(path), map[string]string{manifestFileName: "apps:\n- name: alpha\n  path: .\n  access: {server: {}}\n"})
			parsed, err := resolveManifestFile(filepath.Join(filepath.Dir(path), manifestFileName))
			if err != nil {
				t.Fatal(err)
			}
			resolved, err := resolveManifestGraph(t.Context(), parsed)
			if err != nil {
				t.Fatal(err)
			}
			if !cmp.Equal(resolved.Apps[0].resolved.Egress, test.want) {
				t.Fatalf("effective egress = %#v, want %#v", resolved.Apps[0].resolved.Egress, test.want)
			}
		})
	}
}
