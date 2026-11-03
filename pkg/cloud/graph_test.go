package cloud

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/holon-sh/cli/pkg/protocol"
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

func descriptorRegistry(t *testing.T, document map[string]any) *registrySource {
	t.Helper()
	catalog := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"server": document})
	}))
	t.Cleanup(catalog.Close)
	return &registrySource{RegistryRef: serverReference{
		Name: document["name"].(string), Version: document["version"].(string), Registry: catalog.URL,
	}}
}

func writeDescriptor(t *testing.T, path string, document map[string]any) {
	t.Helper()
	data, _ := json.Marshal(document)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
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
	ref := serverReference{Name: "io.github.acme/beta", Version: "1.4.2", Registry: catalog.URL}
	alpha := descriptorDocument("alpha")
	delete(alpha, "packages")
	alpha["remotes"] = []any{map[string]any{"type": "streamable-http", "url": "https://mcp.example/mcp"}}
	alpha["_meta"] = map[string]any{publisherMetadata: map[string]any{
		"sh.holon": map[string]any{"requires": map[string]serverReference{"beta": ref}},
	}}
	writeDescriptor(t, filepath.Join(root, "alpha/server.json"), alpha)
	policy := &protocol.AppAccess{Server: &protocol.AccessRule{}}
	manifest := manifestSpec{Apps: []manifestApp{
		{Name: "alpha", Path: filepath.Join(root, "alpha"), Access: policy},
		{Name: "beta", Registry: &registrySource{RegistryRef: ref}, Access: policy},
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
	cycleBeta := descriptorDocument("beta")
	cycleBeta["_meta"] = map[string]any{publisherMetadata: map[string]any{
		"sh.holon": map[string]any{"requires": map[string]serverReference{
			"alpha": {Name: "io.github.acme/alpha", Version: "1.4.2"},
		}},
	}}
	writeDescriptor(t, filepath.Join(root, "beta-cycle/server.json"), cycleBeta)
	conflictGamma := descriptorDocument("gamma")
	conflictGamma["_meta"] = map[string]any{publisherMetadata: map[string]any{
		"sh.holon": map[string]any{"requires": map[string]serverReference{
			"beta": {Name: "io.github.acme/beta", Version: "9.0.0"},
		}},
	}}
	writeDescriptor(t, filepath.Join(root, "gamma/server.json"), conflictGamma)
	secretAlpha := descriptorDocument("alpha")
	secretAlpha["_meta"] = alpha["_meta"]
	secretAlpha["packages"].([]any)[0].(map[string]any)["environmentVariables"] = []any{
		map[string]any{"name": "BETA_MCP_URL", "isSecret": true},
	}
	writeDescriptor(t, filepath.Join(root, "alpha-secret/server.json"), secretAlpha)
	writeDescriptor(t, filepath.Join(root, "invalid/server.json"), map[string]any{})
	invalidMetadata := descriptorDocument("invalid")
	invalidMetadata["_meta"] = map[string]any{publisherMetadata: map[string]any{
		"sh.holon": map[string]any{"egress": json.RawMessage("null")},
	}}
	writeDescriptor(t, filepath.Join(root, "invalid-metadata/server.json"), invalidMetadata)
	for _, test := range []struct {
		name, want string
		change     func(*manifestSpec)
	}{
		{name: "missing directory", change: func(p *manifestSpec) {
			p.Apps = []manifestApp{{Name: "example", Path: filepath.Join(root, "missing")}}
		}},
		{name: "file source", change: func(p *manifestSpec) {
			p.Apps = []manifestApp{{Name: "example", Path: filepath.Join(root, "beta/server.json")}}
		}},
		{name: "invalid descriptor", change: func(p *manifestSpec) {
			p.Apps = []manifestApp{{Name: "example", Path: filepath.Join(root, "invalid")}}
		}},
		{name: "invalid publisher metadata", want: "sh.holon metadata", change: func(p *manifestSpec) {
			p.Apps = []manifestApp{{Name: "example", Path: filepath.Join(root, "invalid-metadata")}}
		}},
		{name: "missing dependency", want: "must be declared", change: func(p *manifestSpec) { p.Apps = p.Apps[:1] }},
		{name: "dependency without server access", want: "explicit access.server", change: func(p *manifestSpec) { p.Apps[1].Access = nil }},
		{name: "different dependency descriptor", want: "does not match", change: func(p *manifestSpec) {
			p.Apps[1] = manifestApp{Name: "beta", Path: filepath.Join(root, "alpha"), Access: policy}
		}},
		{name: "dependency cycle", want: "cycle", change: func(p *manifestSpec) {
			p.Apps[1].Path = filepath.Join(root, "beta-cycle")
		}},
		{name: "conflicting dependency references", want: "conflicting references", change: func(p *manifestSpec) {
			p.Apps = append(p.Apps, manifestApp{Name: "gamma", Path: filepath.Join(root, "gamma"), Access: policy})
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

func TestDescriptorEgressAndManifestOverride(t *testing.T) {
	appEgress := []any{map[string]any{"host": "API.Example.com.", "ports": []int{8443, 443, 443}}}
	normalized := protocol.Egress{{Host: "api.example.com", Ports: []int32{443, 8443}}}
	manifestEgress := protocol.Egress{{Host: "manifest.example.com", Ports: []int32{443}}}
	for _, test := range []struct {
		name           string
		metadata       any
		manifestEgress string
		remote         bool
		want           protocol.Egress
	}{
		{name: "app declaration", metadata: appEgress, want: normalized},
		{
			name:           "IP and templated destinations",
			manifestEgress: "[{host: 'fd00:0::3', ports: [3307]}, {host: 'fe80::3', ports: [3307]}, {host: '{DATABASE_IP}', ports: [3307]}]",
			want: protocol.Egress{
				{Host: "fd00::3", Ports: []int32{3307}},
				{Host: "fe80::3", Ports: []int32{3307}},
				{Host: "{DATABASE_IP}", Ports: []int32{3307}},
			},
		},
		{name: "manifest replacement", metadata: appEgress, manifestEgress: "[{host: manifest.example.com, ports: [443]}]", want: manifestEgress},
		{name: "manifest deny all", metadata: appEgress, manifestEgress: "[]", want: protocol.Egress{}},
		{name: "app deny all", metadata: []any{}, want: protocol.Egress{}},
		{name: "no declaration"},
		{name: "remote ignores container egress", metadata: appEgress, remote: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			document := descriptorDocument("alpha")
			document["packages"].([]any)[0].(map[string]any)["environmentVariables"] = []any{map[string]any{"name": "DATABASE_IP"}}
			if test.metadata != nil {
				document["_meta"] = map[string]any{publisherMetadata: map[string]any{
					"sh.holon": map[string]any{"egress": test.metadata},
				}}
			}
			path := filepath.Join(t.TempDir(), "server.json")
			writeDescriptor(t, path, document)
			manifest := "apps:\n- name: alpha\n  path: .\n  access: {server: {}}\n"
			if test.manifestEgress != "" {
				manifest += "  egress: " + test.manifestEgress + "\n"
			}
			sourceFiles(t, filepath.Dir(path), map[string]string{manifestFileName: manifest})
			parsed, err := resolveManifestFile(filepath.Join(filepath.Dir(path), manifestFileName))
			if err != nil {
				t.Fatal(err)
			}
			app := &parsed.Apps[0]
			if test.remote {
				delete(document, "packages")
				document["remotes"] = []any{map[string]any{"type": "streamable-http", "url": "https://mcp.example/mcp"}}
				app.Path, app.Registry = "", descriptorRegistry(t, document)
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
