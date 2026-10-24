package typegen

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/holon-sh/cli/pkg/mcpcontract"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/content/memory"
)

// The fixture serves the same content-addressed index, artifact, and layer over
// OCI HTTP and the in-memory content API, without a Docker daemon or registry.
type contractImage struct {
	store      *memory.Store
	descriptor ocispec.Descriptor
	documents  map[string][]byte
	mediaTypes map[string]string
}

func newContractImage(t *testing.T, metadata any, mutate func(*ocispec.Manifest, *ocispec.Index)) *contractImage {
	t.Helper()
	image := &contractImage{store: memory.New(), documents: map[string][]byte{}, mediaTypes: map[string]string{}}
	add := func(mediaType string, value any) ocispec.Descriptor {
		data := mustJSON(t, value)
		descriptor := content.NewDescriptorFromBytes(mediaType, data)
		if err := image.store.Push(t.Context(), descriptor, bytes.NewReader(data)); err != nil {
			t.Fatal(err)
		}
		image.documents[descriptor.Digest.String()] = data
		image.mediaTypes[descriptor.Digest.String()] = mediaType
		return descriptor
	}
	layer := add(mcpcontract.MediaType, metadata)
	artifact := ocispec.Manifest{ArtifactType: mcpcontract.MediaType, Layers: []ocispec.Descriptor{layer}}
	artifact.SchemaVersion = 2
	index := ocispec.Index{MediaType: ocispec.MediaTypeImageIndex}
	index.SchemaVersion = 2
	if mutate != nil {
		mutate(&artifact, &index)
	}
	child := add(ocispec.MediaTypeImageManifest, artifact)
	child.ArtifactType = mcpcontract.MediaType
	if index.Manifests == nil {
		index.Manifests = []ocispec.Descriptor{child}
	}
	image.descriptor = add(index.MediaType, index)
	return image
}

func (image *contractImage) client(t *testing.T) *http.Client {
	t.Helper()
	return &http.Client{Transport: registryResponse(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host != "images.example" || !strings.HasPrefix(request.URL.Path, "/v2/orders/") {
			t.Errorf("image dependency requested a different registry: %s", request.URL)
			return nil, fmt.Errorf("unexpected registry request: %s", request.URL)
		}
		key := request.URL.Path[strings.LastIndex(request.URL.Path, "/")+1:]
		if key == "1.0.0" {
			key = image.descriptor.Digest.String()
		}
		data, ok := image.documents[key]
		if !ok {
			return nil, fmt.Errorf("unexpected OCI document: %s", request.URL)
		}
		header := make(http.Header)
		header.Set("Content-Type", image.mediaTypes[key])
		header.Set("Content-Length", strconv.Itoa(len(data)))
		header.Set("Docker-Content-Digest", key)
		return &http.Response{StatusCode: http.StatusOK, Header: header, Body: io.NopCloser(bytes.NewReader(data)), ContentLength: int64(len(data)), Request: request}, nil
	})}
}

func ordersMetadata() *mcpcontract.Metadata {
	return &mcpcontract.Metadata{Version: mcpcontract.Version,
		Identity: mcpcontract.Identity{Name: "example.com/orders", Version: "1.0.0"},
		Contract: &mcpcontract.Contract{Tools: []mcpcontract.Tool{{Name: "lookup", Input: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"]}`), Output: json.RawMessage(`{"type":"string"}`)}}},
	}
}

func TestReadTypeMetadataValidatesOCIArtifacts(t *testing.T) {
	for _, test := range []struct {
		name            string
		mutate          func(*ocispec.Manifest, *ocispec.Index)
		invalidMetadata bool
		wantError       string
		untyped         bool
	}{
		{name: "OCI index"},
		{name: "Docker index", mutate: func(_ *ocispec.Manifest, index *ocispec.Index) {
			index.MediaType = "application/vnd.docker.distribution.manifest.list.v2+json"
		}},
		{name: "no contract", mutate: func(_ *ocispec.Manifest, index *ocispec.Index) { index.Manifests = []ocispec.Descriptor{} }, untyped: true},
		{name: "invalid artifact", mutate: func(artifact *ocispec.Manifest, _ *ocispec.Index) { artifact.Layers = nil }, wantError: "invalid MCP contract artifact"},
		{name: "invalid metadata", invalidMetadata: true, wantError: "mcptype"},
	} {
		t.Run(test.name, func(t *testing.T) {
			metadata := ordersMetadata()
			if test.invalidMetadata {
				metadata.Version = 999
			}
			image := newContractImage(t, metadata, test.mutate)
			result, err := readTypeMetadata(t.Context(), image.store, image.descriptor)
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("invalid contract accepted: result=%+v error=%v", result, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if test.untyped {
				if result != nil {
					t.Fatal("image without a contract became typed")
				}
				return
			}
			if result == nil || result.Identity != metadata.Identity || result.Contract == nil || len(result.Contract.Tools) != 1 || result.Contract.Tools[0].Name != "lookup" {
				t.Fatalf("retained contract not read: %+v", result)
			}
		})
	}
}

func TestResolveTypeMetadataRequiresExplicitImageReferences(t *testing.T) {
	image := newContractImage(t, ordersMetadata(), nil)
	options := Options{HTTPClient: image.client(t), Env: []string{"DOCKER_CONFIG=" + t.TempDir()}}
	for _, ref := range []string{"images.example/orders:1.0.0", "images.example/orders@" + image.descriptor.Digest.String()} {
		metadata, err := resolveTypeMetadata(t.Context(), ref, options)
		if err != nil || metadata == nil || metadata.Identity != ordersMetadata().Identity {
			t.Fatalf("resolve %s: metadata=%+v error=%v", ref, metadata, err)
		}
	}
	for _, ref := range []string{"orders:1.0.0", "images.example/orders", "images.example/orders@sha256:invalid"} {
		if _, err := resolveTypeMetadata(t.Context(), ref, options); err == nil {
			t.Fatalf("accepted unpinned or invalid image %q", ref)
		}
	}
}
