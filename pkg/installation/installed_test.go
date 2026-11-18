package installation

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/holon-sh/cli/pkg/installation/installationtest"
	"github.com/holon-sh/cli/pkg/mcpcontract"
	"github.com/holon-sh/cli/pkg/mcpregistry"
	"github.com/holon-sh/cli/pkg/typegen"
	"github.com/holon-sh/protocol"
)

func TestDeployTypesCallsToAppsOnTheInstallation(t *testing.T) {
	t.Parallel()
	var built typegen.Served
	if err := json.Unmarshal([]byte(servedContract), &built); err != nil {
		t.Fatal(err)
	}
	pinned, err := built.Document()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ name, contract, want string }{
		{name: "an app on the installation is typed and stays out of the deployment", contract: servedContract},
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

func TestDevTypesCallsToAppsOnTheSession(t *testing.T) {
	f := startReloadFixture(t, false, map[string]string{
		manifestFileName:     gammaManifest,
		"gamma/server.json":  strings.Replace(requiresAlpha, `"alpha"`, `"pokeapi"`, 1),
		"gamma/server.ts":    "import { apps } from \"#holon-sh\";\nimport { McpServer } from \"@modelcontextprotocol/server\";\n",
		"gamma/package.json": `{"private":true,"dependencies":{"holon-sh":"0.0.1"}}`,
	})
	f.await(func() bool { return f.created() == 1 })
	if apps := f.apps(); !slices.Equal(apps, []string{"alpha", "gamma"}) {
		t.Fatalf("dev created a session with %v", apps)
	}
	f.update(func() {
		f.served = append(f.served, protocol.App{Name: "pokeapi", Inherited: true, Remote: &protocol.Remote{URL: "https://pokeapi.example/mcp"}})
		f.ready = true
	})
	f.watching()
	sent := f.reloadRequests()
	gamma := sent[slices.IndexFunc(sent, func(sent sentSource) bool { return sent.App == "gamma" })]
	if client := string(overlayEntries(t, gamma.body)["generated/holon-sh/index.mjs"]); !strings.Contains(client, "lookup_pokeapi") {
		t.Fatalf("gamma started without a client for pokeapi: %q", client)
	}
	f.save(map[string]string{"gamma/server.json": strings.Replace(requiresAlpha, `"alpha":{}`, `"pokeapi":{},"missing":{}`, 1)},
		"gamma requires missing, which is neither an app in this project nor on the installation")
}
