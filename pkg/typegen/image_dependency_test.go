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
	"strconv"
	"strings"
	"testing"

	"github.com/holon-sh/cli/pkg/mcpcontract"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
)

func imageCaller(t *testing.T, provider App) Manifest {
	t.Helper()
	root := t.TempDir()
	writeProvider(t, filepath.Join(root, "caller"), "caller", "from holon.apps import apps\n"+pythonProvider, map[string]Reference{
		"orders": {Name: "example.com/orders", Version: "1.0.0", Registry: "https://mcp.example"},
	})
	provider.Name = "orders"
	provider.Access = json.RawMessage(`{"server":{}}`)
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
	for _, test := range []struct{ name, identity, artifactType, wantError string }{
		{"typed image", "example.com/orders", mcpcontract.MediaType, ""},
		{"wrong identity", "example.com/wrong", mcpcontract.MediaType, "image contract belongs to"},
		{"invalid artifact", "example.com/orders", "invalid", "invalid MCP contract artifact"},
	} {
		t.Run(test.name, func(t *testing.T) {
			metadata := &mcpcontract.Metadata{Version: mcpcontract.Version,
				Identity: mcpcontract.Identity{Name: test.identity, Version: "1.0.0"},
				Contract: &mcpcontract.Contract{Tools: []mcpcontract.Tool{{Name: "lookup", Input: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"]}`)}}},
			}
			var diagnostics bytes.Buffer
			prepared, err := Prepare(t.Context(), imageCaller(t, App{Image: "images.example/orders:1.0.0"}), Options{Apps: []string{"caller"}, HTTPClient: imageClient(t, metadata, test.artifactType), Env: []string{"DOCKER_CONFIG=" + t.TempDir()}, Stderr: &diagnostics})
			if prepared != nil {
				defer prepared.Close()
			}
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("wanted %q, got %v", test.wantError, err)
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
	for _, provider := range []App{
		{Image: "images.example/orders:1.0.0"},
		{Registry: &RegistrySource{Reference: Reference{Name: "example.com/orders", Version: "1.0.0", Registry: "https://mcp.example"}}},
	} {
		for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound} {
			t.Run(fmt.Sprintf("registry=%t/status=%d", provider.Registry != nil, status), func(t *testing.T) {
				client := &http.Client{Transport: registryResponse(func(request *http.Request) (*http.Response, error) {
					code, body := status, `{"errors":[{"code":"DENIED","message":"private image"}]}`
					if request.URL.Host == "mcp.example" && provider.Registry != nil {
						code, body = http.StatusOK, `{"server":{"name":"example.com/orders","version":"1.0.0","description":"Orders","packages":[{"registryType":"oci","identifier":"images.example/orders:1.0.0"}]}}`
					} else if request.URL.Host != "images.example" {
						t.Errorf("unexpected registry request: %s", request.URL)
					}
					return &http.Response{StatusCode: code, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
				})}
				var diagnostics bytes.Buffer
				prepared, err := Prepare(t.Context(), imageCaller(t, provider), Options{Apps: []string{"caller"}, HTTPClient: client, Env: []string{"DOCKER_CONFIG=" + t.TempDir()}, Stderr: &diagnostics})
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

// Serve the retained contract through OCI HTTP so Prepare exercises the real
// image resolver, including artifact validation and content digests.
func imageClient(t *testing.T, metadata *mcpcontract.Metadata, artifactType string) *http.Client {
	t.Helper()
	type document struct {
		descriptor ocispec.Descriptor
		data       []byte
	}
	documents := map[string]document{}
	add := func(mediaType string, value any) ocispec.Descriptor {
		data := mustJSON(t, value)
		descriptor := content.NewDescriptorFromBytes(mediaType, data)
		documents[descriptor.Digest.String()] = document{descriptor, data}
		return descriptor
	}
	layer := add(mcpcontract.MediaType, metadata)
	artifact := ocispec.Manifest{ArtifactType: artifactType, Layers: []ocispec.Descriptor{layer}}
	artifact.SchemaVersion = 2
	child := add(ocispec.MediaTypeImageManifest, artifact)
	child.ArtifactType = mcpcontract.MediaType
	index := ocispec.Index{Manifests: []ocispec.Descriptor{child}}
	index.SchemaVersion = 2
	descriptor := add(ocispec.MediaTypeImageIndex, index)
	documents["1.0.0"] = documents[descriptor.Digest.String()]
	return &http.Client{Transport: registryResponse(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host != "images.example" || !strings.HasPrefix(request.URL.Path, "/v2/orders/") {
			t.Errorf("image dependency requested a different registry: %s", request.URL)
			return nil, fmt.Errorf("unexpected registry request: %s", request.URL)
		}
		doc, ok := documents[filepath.Base(request.URL.Path)]
		if !ok {
			return nil, fmt.Errorf("unexpected OCI document: %s", request.URL)
		}
		header := make(http.Header)
		header.Set("Content-Type", doc.descriptor.MediaType)
		header.Set("Content-Length", strconv.Itoa(len(doc.data)))
		header.Set("Docker-Content-Digest", doc.descriptor.Digest.String())
		return &http.Response{StatusCode: http.StatusOK, Header: header, Body: io.NopCloser(bytes.NewReader(doc.data)), ContentLength: int64(len(doc.data)), Request: request}, nil
	})}
}
