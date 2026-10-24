package typegen

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func imageCaller(t *testing.T, registry bool) Manifest {
	t.Helper()
	root := t.TempDir()
	writeProvider(t, filepath.Join(root, "caller"), "caller", "from holon.apps import apps\n"+pythonProvider, map[string]Reference{
		"orders": {Name: "example.com/orders", Version: "1.0.0", Registry: "https://mcp.example"},
	})
	provider := App{Name: "orders", Image: "images.example/orders:1.0.0", Access: json.RawMessage(`{"server":{}}`)}
	if registry {
		provider.Image = ""
		provider.Registry = &RegistrySource{Reference: Reference{Name: "example.com/orders", Version: "1.0.0", Registry: "https://mcp.example"}}
	}
	put(t, root, "holon.yaml", string(mustJSON(t, Manifest{Apps: []App{{Name: "caller", Path: "caller"}, provider}})))
	manifest, err := LoadManifest(filepath.Join(root, "holon.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}

func TestPrepareResolvesDeclaredImageContract(t *testing.T) {
	if _, err := typeLanguages["python"].localProgram(context.Background()); err != nil {
		t.Skip(err)
	}
	for _, test := range []struct {
		name                           string
		wrongIdentity, invalidArtifact bool
	}{
		{name: "typed image"},
		{name: "wrong identity", wrongIdentity: true},
		{name: "invalid artifact", invalidArtifact: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			metadata := ordersMetadata()
			if test.wrongIdentity {
				metadata.Identity.Name = "example.com/wrong"
			}
			image := newContractImage(t, metadata, func(artifact *ocispec.Manifest, _ *ocispec.Index) {
				if test.invalidArtifact {
					artifact.Layers = nil
				}
			})
			var diagnostics bytes.Buffer
			prepared, err := Prepare(t.Context(), imageCaller(t, false), Options{Apps: []string{"caller"}, HTTPClient: image.client(t), Env: []string{"DOCKER_CONFIG=" + t.TempDir()}, Stderr: &diagnostics})
			if prepared != nil {
				defer prepared.Close()
			}
			if test.wrongIdentity || test.invalidArtifact {
				if err == nil {
					t.Fatal("invalid image contract degraded to an untyped client")
				}
				want := "image contract belongs to"
				if test.invalidArtifact {
					want = "invalid MCP contract artifact"
				}
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if diagnostics.Len() != 0 {
				t.Fatalf("typed image was unavailable: %s", &diagnostics)
			}
			expected, ok := prepared.Metadata()["caller"].Requires["orders"]
			if !ok || expected.Identity != metadata.Identity || len(expected.Contract.Tools) != 1 || expected.Contract.Tools[0].Name != "lookup" {
				t.Fatalf("image contract was not retained in caller expectations: %+v", expected)
			}
			module := prepared.Manifest.Apps[0].prepared.packages["python"]["holon/apps/__init__.py"]
			if !strings.Contains(module, "async def lookup(") {
				t.Fatalf("image client is not typed:\n%s", module)
			}
		})
	}
}

func TestPrepareDegradesUnavailableOCIContracts(t *testing.T) {
	if _, err := typeLanguages["python"].localProgram(context.Background()); err != nil {
		t.Skip(err)
	}
	for _, registry := range []bool{false, true} {
		for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound} {
			t.Run(fmt.Sprintf("registry=%t/status=%d", registry, status), func(t *testing.T) {
				client := &http.Client{Transport: registryResponse(func(request *http.Request) (*http.Response, error) {
					code, body := status, `{"errors":[{"code":"DENIED","message":"private image"}]}`
					if request.URL.Host == "mcp.example" && registry {
						code, body = http.StatusOK, `{"server":{"name":"example.com/orders","version":"1.0.0","description":"Orders","packages":[{"registryType":"oci","identifier":"images.example/orders:1.0.0"}]}}`
					} else if request.URL.Host != "images.example" {
						t.Errorf("unexpected registry request: %s", request.URL)
					}
					return &http.Response{StatusCode: code, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
				})}
				var diagnostics bytes.Buffer
				prepared, err := Prepare(t.Context(), imageCaller(t, registry), Options{Apps: []string{"caller"}, HTTPClient: client, Env: []string{"DOCKER_CONFIG=" + t.TempDir()}, Stderr: &diagnostics})
				if err != nil {
					t.Fatalf("unavailable OCI contract blocked generation: %v", err)
				}
				defer prepared.Close()
				if !strings.Contains(diagnostics.String(), "typed dependency orders unavailable:") {
					t.Fatalf("missing diagnostic: %s", &diagnostics)
				}
				if len(prepared.Metadata()["caller"].Requires) != 0 {
					t.Fatal("unavailable image advertised typed expectations")
				}
				module := prepared.Manifest.Apps[0].prepared.packages["python"]["holon/apps/__init__.py"]
				if !strings.Contains(module, "orders") || !strings.Contains(module, "async def call_tool(self, name: _builtins.str") {
					t.Fatalf("untyped client missing:\n%s", module)
				}
			})
		}
	}
}

func TestImageDependencyCancellationRemainsAnError(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	client := &http.Client{Transport: registryResponse(func(*http.Request) (*http.Response, error) {
		cancel()
		return nil, context.Canceled
	})}
	_, _, err := resolveRequired(ctx, "orders", Reference{Name: "example.com/orders", Version: "1.0.0"}, nil,
		map[string]App{"orders": {Image: "images.example/orders:1.0.0"}},
		Options{HTTPClient: client, Env: []string{"DOCKER_CONFIG=" + t.TempDir()}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled OCI request degraded to untyped: %v", err)
	}
}
