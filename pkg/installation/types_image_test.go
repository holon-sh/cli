package installation

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/holon-sh/cli/pkg/mcpcontract"
	"github.com/holon-sh/cli/pkg/mcpregistry"
	"github.com/holon-sh/cli/pkg/typegen"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"

	"github.com/holon-sh/protocol"
)

func TestImageContractsSurviveImport(t *testing.T) {
	t.Parallel()
	fixture, server := buildRegistry(t)
	host := strings.TrimPrefix(server.URL, "https://")
	options := buildOptions{HTTPClient: server.Client(), Env: []string{"DOCKER_CONFIG=" + t.TempDir()}, Stderr: io.Discard}
	raw := []byte(` { "schema":{"tools":[{"name":"lookup",
"inputSchema":{"type":"object","properties":{"id":{"type":"string"}},"required":["id"]}}]},
"identity":{"version":"1","name":"example/orders"} } `)
	expected, err := typegen.ParseDocument(t.Context(), raw)
	if err != nil {
		t.Fatal(err)
	}
	descriptor := &mcpregistry.Descriptor{Name: "example/checkout", Version: "1"}
	caller := &mcpcontract.Metadata{Identity: descriptor.Identity(), Requires: map[string]string{"orders": expected.Digest}}
	image := publishTypeMetadata(t, fixture, host+"/checkout", caller, options, map[string]*typegen.Document{expected.Digest: expected})
	copied, err := importImage(t.Context(), image, host, "destination/checkout", "", options.HTTPClient)
	if err != nil {
		t.Fatal(err)
	}
	fixture.mu.Lock()
	for key := range fixture.items {
		if strings.HasPrefix(key, "checkout/") {
			delete(fixture.items, key)
		}
	}
	fixture.mu.Unlock()
	for _, test := range []struct{ name, version, input, want string }{
		{"same release", "1", `{"type":"object","properties":{"id":{"type":"string"}},"required":["id"]}`, ""},
		{"changed input", "1", `{"type":"object","properties":{"id":{"type":"number"}},"required":["id"]}`, "tool lookup input changed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			published := &mcpcontract.Metadata{
				Identity: mcpcontract.Identity{Name: "example/orders", Version: test.version},
				Schema:   &mcpcontract.Contract{Tools: []mcpcontract.Tool{{Name: "lookup", Input: json.RawMessage(test.input)}}},
			}
			provider := publishTypeMetadata(t, fixture, host+"/orders", published, options, nil)
			deployment := manifestSpec{Apps: []manifestApp{
				{Name: "orders", resolved: resolvedApp{App: protocol.App{Name: "orders", Image: provider}}},
				{Name: "checkout", resolved: resolvedApp{App: protocol.App{Name: "checkout", Image: copied}, descriptor: descriptor}},
			}}
			_, cleanup, err := prepareManifestTypes(t.Context(), deployment, options)
			if test.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				cleanup()
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.want) || !strings.Contains(err.Error(), "holon deploy orders checkout") {
				t.Fatalf("expectation check: %v", err)
			}
		})
	}
	fixture.mu.Lock()
	delete(fixture.items, "destination/checkout/blobs/"+expected.Digest)
	fixture.mu.Unlock()
	deployment := manifestSpec{Apps: []manifestApp{{Name: "checkout", resolved: resolvedApp{App: protocol.App{Image: copied}}}}}
	_, _, err = prepareManifestTypes(t.Context(), deployment, options)
	if err == nil || !strings.Contains(err.Error(), "fetch expected orders contract "+expected.Digest) {
		t.Fatalf("missing pinned blob: %v", err)
	}
}

func TestLiveDependencySnapshotIsPinnedByConsumer(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "live-orders", Version: "1"}, nil)
	tool := &mcp.Tool{Name: "lookup", InputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}}}`)}
	server.AddTool(tool, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t.Error("preparation executed the dependency tool")
		return &mcp.CallToolResult{}, nil
	})
	live := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	defer live.Close()
	root := t.TempDir()
	sourceFiles(t, root, map[string]string{
		"server.js": "export default {}", "package.json": `{"dependencies":{"holon-sh":"*"}}`,
	})
	manifest := typedManifest(t,
		manifestApp{Name: "orders", resolved: resolvedApp{
			App: protocol.App{Name: "orders", Remote: &protocol.Remote{URL: live.URL}}, descriptor: &mcpregistry.Descriptor{Name: "example/orders", Version: "1"},
		}},
		manifestApp{Name: "checkout", Path: root, resolved: resolvedApp{
			App: protocol.App{Name: "checkout", Requires: []string{"orders"}}, descriptor: &mcpregistry.Descriptor{Name: "example/checkout", Version: "1"},
		}},
	)
	prepared, cleanup, err := prepareManifestTypes(t.Context(), manifest, buildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	caller := prepared.Apps[1].resolved.types
	stored := caller.documents[caller.metadata.Requires["orders"]]
	if stored == nil || stored.Identity != (mcpcontract.Identity{Name: "example/orders", Version: "1"}) || len(stored.Schema.Tools) != 1 {
		t.Fatalf("consumer did not pin the live snapshot: %+v", stored)
	}
}

func publishTypeMetadata(
	t *testing.T, fixture *imageRegistry, image string, metadata *mcpcontract.Metadata, options buildOptions, documents map[string]*typegen.Document,
) string {
	t.Helper()
	repository, err := imageRepository(image, "", options.HTTPClient)
	if err != nil {
		t.Fatal(err)
	}
	artifact, ownDigest, err := pushTypeMetadata(t.Context(), repository, metadata, documents)
	if err != nil {
		t.Fatal(err)
	}
	if artifact.Platform == nil || artifact.Platform.OS != "unknown" || artifact.Platform.Architecture != "unknown" {
		t.Fatalf("contract manifest must not advertise an executable platform: %+v", artifact.Platform)
	}
	fixture.mu.Lock()
	platform := addTestImage(t, fixture, "amd64", ocispec.ImageConfig{}, nil)
	for key, item := range fixture.items {
		if path, ok := strings.CutPrefix(key, "source/"); ok {
			fixture.items[repository.Reference.Repository+"/"+path] = item
		}
	}
	fixture.mu.Unlock()
	data, _ := json.Marshal(ocispec.Index{
		Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageIndex,
		Manifests: []ocispec.Descriptor{platform, artifact}, Annotations: map[string]string{mcpcontract.DigestAnnotation: ownDigest},
	})
	index, err := oras.PushBytes(t.Context(), repository, ocispec.MediaTypeImageIndex, data)
	if err != nil {
		t.Fatal(err)
	}
	return image + "@" + index.Digest.String()
}
