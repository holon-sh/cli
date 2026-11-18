package installation

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/holon-sh/cli/pkg/installation/installationtest"
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
	options := buildOptions{HTTPClient: server.Client(), Env: []string{"DOCKER_CONFIG=" + t.TempDir()}}
	raw := []byte(` { "schema":{"tools":[{"name":"lookup",
"inputSchema":{"type":"object","properties":{"id":{"type":"string"}},"required":["id"]}}]},
"identity":{"version":"1","name":"example/orders"} } `)
	expected, err := typegen.ParseDocument(raw)
	if err != nil {
		t.Fatal(err)
	}
	descriptor := &mcpregistry.Descriptor{Name: "example/checkout", Version: "1"}
	caller := &mcpcontract.Metadata{Identity: descriptor.Identity(), Requires: map[string]string{"orders": expected.Digest}}
	image := publishTypeMetadata(t, fixture, host+"/checkout", caller, options, map[string]*typegen.Document{expected.Digest: expected})
	destination, err := imageRepository(host+"/destination/checkout", "", options.HTTPClient)
	if err != nil {
		t.Fatal(err)
	}
	imported, err := importImage(t.Context(), image, destination, options.HTTPClient)
	if err != nil {
		t.Fatal(err)
	}
	copied := destination.Reference.String() + "@" + imported.Digest.String()
	fixture.mu.Lock()
	for key := range fixture.items {
		if strings.HasPrefix(key, "checkout/") {
			delete(fixture.items, key)
		}
	}
	fixture.mu.Unlock()
	published := &mcpcontract.Metadata{
		Identity: mcpcontract.Identity{Name: "example/orders", Version: "1"},
		Schema:   &mcpcontract.Contract{Tools: []mcpcontract.Tool{{Name: "lookup", Input: json.RawMessage(`{"type":"object","properties":{"id":{"type":"number"}},"required":["id"]}`)}}},
	}
	deployment := manifestSpec{Apps: []manifestApp{
		{Name: "orders", resolved: resolvedApp{App: protocol.App{Name: "orders", Image: publishTypeMetadata(t, fixture, host+"/orders", published, options, nil)}}},
		{Name: "checkout", resolved: resolvedApp{App: protocol.App{Name: "checkout", Image: copied}, descriptor: descriptor}},
	}}
	_, _, err = prepareManifestTypes(t.Context(), deployment, options, nil)
	if err == nil || !strings.Contains(err.Error(), "tool lookup input changed") || !strings.Contains(err.Error(), "holon deploy orders checkout") {
		t.Fatalf("expectation check: %v", err)
	}
	fixture.mu.Lock()
	delete(fixture.items, "destination/checkout/blobs/"+expected.Digest)
	fixture.mu.Unlock()
	deployment = manifestSpec{Apps: []manifestApp{{Name: "checkout", resolved: resolvedApp{App: protocol.App{Image: copied}}}}}
	_, _, err = prepareManifestTypes(t.Context(), deployment, options, nil)
	if err == nil || !strings.Contains(err.Error(), "fetch expected orders contract "+expected.Digest) {
		t.Fatalf("missing pinned blob: %v", err)
	}
}

func TestDeployTypesCallsToAppsOnTheInstallation(t *testing.T) {
	t.Parallel()
	var built typegen.Served
	if err := json.Unmarshal([]byte(servedContract), &built); err != nil {
		t.Fatal(err)
	}
	pinned, err := built.Document("pokeapi")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ name, contract, want string }{
		{
			name:     "an app on the installation, however long its catalog, is typed and stays out of the deployment",
			contract: strings.Replace(servedContract, `"name":"lookup"`, `"name":"lookup","description":"`+strings.Repeat("a", maxHTTPBody)+`"`, 1),
		},
		{
			name: "a contract that changed on the installation stops the deployment", contract: strings.Replace(servedContract, `"lookup"`, `"search"`, 1),
			want: "pokeapi has changed the typed contract expected by checkout (tool lookup removed); rebuild and publish the caller",
		},
		{
			name: "an app in neither place stops the deployment",
			want: "checkout requires pokeapi, which is neither an app in this project nor on the installation; declare it in holon.yaml or add it to the installation",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			h := newRunHarness(t)
			document := descriptorDocument("checkout")
			identity := mcpcontract.Identity{Name: document["name"].(string), Version: document["version"].(string)}
			document["packages"].([]any)[0].(map[string]any)["identifier"] = publishTypeMetadata(t, h.registry,
				strings.TrimPrefix(h.registryServer.URL, "https://")+"/checkout", &mcpcontract.Metadata{Identity: identity, Requires: map[string]string{"pokeapi": pinned.Digest}},
				buildOptions{HTTPClient: h.registryServer.Client()}, map[string]*typegen.Document{pinned.Digest: pinned})
			document["_meta"] = map[string]any{mcpregistry.PublisherMetadata: map[string]any{"sh.holon": map[string]any{"requires": map[string]any{"pokeapi": map[string]any{}}}}}
			catalog := descriptorRegistry(t, document)
			root := t.TempDir()
			sourceFiles(t, root, map[string]string{manifestFileName: "apps:\n- name: checkout\n  access: {server: {}}\n" +
				"  registry: {name: " + identity.Name + ", version: " + identity.Version + ", registry: '" + catalog.Registry + "'}\n"})
			var production []protocol.App
			if test.contract != "" {
				production = []protocol.App{{Name: "pokeapi", Remote: &protocol.Remote{URL: "https://pokeapi.example/mcp"}}}
			}
			var mu sync.Mutex
			var desired []protocol.App
			h.mux.HandleFunc("GET "+protocol.APIPathInfo, func(w http.ResponseWriter, _ *http.Request) {
				installationtest.WriteJSON(w, protocol.Info{Installation: protocol.InstallationReference{Origin: h.server.URL, UID: "test-installation"}})
			})
			h.control.Handle([]string{protocol.ToolGetAppContract}, map[string]string{"session": protocol.ProductionName, "app": "pokeapi"},
				func(w http.ResponseWriter, _ *http.Request) {
					installationtest.WriteJSON(w, json.RawMessage(test.contract))
				})
			h.control.Handle([]string{protocol.ToolUploadGrant}, nil, func(w http.ResponseWriter, _ *http.Request) {
				grant := buildGrant(h.registryServer)
				grant.Repositories = map[string]string{"checkout": "holon/upload/checkout"}
				installationtest.WriteJSON(w, grant)
			})
			h.control.Handle([]string{protocol.ToolGetSession, protocol.ToolUpdateSession}, map[string]string{"session": protocol.ProductionName},
				func(w http.ResponseWriter, r *http.Request) {
					mu.Lock()
					defer mu.Unlock()
					session := protocol.SessionResponse{
						Name: protocol.ProductionName, UID: "production-uid", Revision: "1", Generation: 1, Apps: production,
						Conditions: []protocol.Condition{{Type: protocol.ConditionReady, Status: protocol.ConditionTrue}},
					}
					if installationtest.ToolName(r) == protocol.ToolUpdateSession {
						var input protocol.UpdateSessionRequest
						if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
							t.Error(err)
						}
						desired = input.Desired.Apps
						session.Revision, session.Generation, session.Apps = "2", 2, append(slices.Clone(production), desired...)
					}
					installationtest.WriteJSON(w, session)
				})
			h.start(t)
			err := executeCommand(t.Context(), []string{"deploy", "--file", filepath.Join(root, manifestFileName)}, nil, nil, h.run)
			if test.want != "" {
				if err == nil || !strings.Contains(err.Error(), test.want) {
					t.Fatalf("deploy = %v, want %q", err, test.want)
				}
				return
			}
			mu.Lock()
			defer mu.Unlock()
			if err != nil || len(desired) != 1 || desired[0].Name != "checkout" || !slices.Equal(desired[0].Requires, []string{"pokeapi"}) {
				t.Fatalf("deploy sent %+v: %v", desired, err)
			}
		})
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
			App: protocol.App{Name: "orders", Remote: &protocol.Remote{URL: live.URL}},
		}},
		manifestApp{Name: "checkout", Path: root, resolved: resolvedApp{
			App: protocol.App{Name: "checkout", Requires: []string{"orders"}}, descriptor: &mcpregistry.Descriptor{Name: "example/checkout", Version: "1"},
		}},
	)
	prepared, cleanup, err := prepareManifestTypes(t.Context(), manifest, buildOptions{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	caller := prepared.Apps[1].resolved.types
	stored := caller.documents[caller.metadata.Requires["orders"]]
	if stored == nil || stored.Identity != (mcpcontract.Identity{Name: "live-orders", Version: "1"}) || len(stored.Schema.Tools) != 1 {
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
