package installation

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/holon-sh/cli/pkg/installation/installationtest"
	"github.com/holon-sh/cli/pkg/mcpcontract"
	"github.com/holon-sh/cli/pkg/mcpregistry"
	"github.com/holon-sh/cli/pkg/typegen"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"

	"github.com/holon-sh/protocol"
)

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
	for _, test := range []struct {
		name, contract, want        string
		status                      int
		untyped, deployed, declared bool
	}{
		{
			name:     "an app the deployment runs is read however long its catalog",
			contract: strings.Replace(servedContract, `"name":"lookup"`, `"name":"lookup","description":"`+strings.Repeat("a", maxHTTPBody)+`"`, 1),
			deployed: true,
		},
		{
			name: "a contract that changed on the installation stops the deployment", contract: strings.Replace(servedContract, `"lookup"`, `"search"`, 1),
			deployed: true, want: "pokeapi has changed the typed contract expected by checkout (tool lookup removed)",
		},
		{
			name: "an app in neither place stops the deployment of a caller that types no calls to it", status: http.StatusNotFound, untyped: true,
			want: "pokeapi is neither an app in this project nor on the installation",
		},
		{name: "an app the deployment runs is not read when the caller types no calls to it", status: http.StatusNotFound, untyped: true, deployed: true},
		{name: "a contract the deployer cannot read does not stop the deployment", status: http.StatusForbidden, deployed: true},
		{name: "a remote the project declares never stops the deployment of a caller built against it", declared: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			h := newRunHarness(t)
			document := descriptorDocument("checkout")
			identity := mcpcontract.Identity{Name: document["name"].(string), Version: document["version"].(string)}
			metadata := &mcpcontract.Metadata{Identity: identity, Requires: map[string]string{"pokeapi": pinned.Digest}}
			documents := map[string]*typegen.Document{pinned.Digest: pinned}
			if test.untyped {
				metadata.Requires, documents = nil, nil
			}
			production := protocol.SessionResponse{
				Name: protocol.ProductionName, UID: "production-uid", Revision: "1",
				Conditions: []protocol.Condition{{Type: protocol.ConditionReady, Status: protocol.ConditionTrue}},
			}
			if test.deployed {
				production.Apps = []protocol.App{{Name: "pokeapi", Remote: &protocol.Remote{URL: "https://pokeapi.example/mcp"}}}
			}
			document["packages"].([]any)[0].(map[string]any)["identifier"] = publishTypeMetadata(t, h.registry,
				strings.TrimPrefix(h.registryServer.URL, "https://")+"/checkout", metadata, buildOptions{HTTPClient: h.registryServer.Client()}, documents)
			document["_meta"] = map[string]any{mcpregistry.PublisherMetadata: map[string]any{"sh.holon": map[string]any{"requires": map[string]any{"pokeapi": map[string]any{}}}}}
			catalog := descriptorRegistry(t, document)
			root := t.TempDir()
			manifest := "apps:\n- name: checkout\n  access: {server: {}}\n" +
				"  registry: {name: " + identity.Name + ", version: " + identity.Version + ", registry: '" + catalog.Registry + "'}\n"
			if test.declared {
				manifest += "- name: pokeapi\n  remote: {url: https://pokeapi.example/mcp}\n  access: {server: {}}\n"
			}
			sourceFiles(t, root, map[string]string{manifestFileName: manifest})
			h.mux.HandleFunc("GET "+protocol.APIPathInfo, func(w http.ResponseWriter, _ *http.Request) {
				installationtest.WriteJSON(w, protocol.Info{Installation: protocol.InstallationReference{Origin: h.server.URL, UID: "test-installation"}})
			})
			h.control.Handle([]string{protocol.ToolGetAppContract}, map[string]string{"session": protocol.ProductionName, "app": "pokeapi"},
				func(w http.ResponseWriter, _ *http.Request) {
					if test.status != 0 {
						w.WriteHeader(test.status)
						return
					}
					installationtest.WriteJSON(w, json.RawMessage(test.contract))
				})
			h.control.Handle([]string{protocol.ToolUploadGrant}, nil, func(w http.ResponseWriter, _ *http.Request) {
				grant := buildGrant(h.registryServer)
				grant.Repositories = map[string]string{"checkout": "holon/upload/checkout", "pokeapi": "holon/upload/pokeapi"}
				installationtest.WriteJSON(w, grant)
			})
			h.control.Handle([]string{protocol.ToolGetSession, protocol.ToolUpdateSession}, map[string]string{"session": protocol.ProductionName},
				func(w http.ResponseWriter, _ *http.Request) { installationtest.WriteJSON(w, production) })
			h.start(t)
			err := executeCommand(t.Context(), []string{"deploy", "--file", filepath.Join(root, manifestFileName)}, nil, nil, h.run)
			if (err == nil) != (test.want == "") || err != nil && !strings.Contains(err.Error(), test.want) {
				t.Fatalf("deploy = %v, want %q", err, test.want)
			}
		})
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
