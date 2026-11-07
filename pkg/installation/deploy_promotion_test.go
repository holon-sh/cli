package installation

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"github.com/holon-sh/cli/pkg/installation/installationtest"
	"github.com/holon-sh/protocol"
)

func TestDeployReusesDevelopmentImagesBuiltFromTheSameSource(t *testing.T) {
	root := t.TempDir()
	sourceFiles(t, root, map[string]string{
		"server.js": "export default {}", "package.json": sourcePackage,
		manifestFileName: "apps:\n- name: prod\n  access: {server: {}}\n  path: .\n",
	})
	h := newRunHarness(t)
	h.mux.HandleFunc("GET "+protocol.APIPathInfo, func(w http.ResponseWriter, _ *http.Request) {
		installationtest.WriteJSON(w, protocol.Info{
			Installation:          protocol.InstallationReference{Origin: h.server.URL, UID: "test-installation"},
			ImageRepositoryPrefix: "registry.example/apps", BuildArchitectures: []string{"amd64"},
		})
	})
	var mu sync.Mutex
	var development []protocol.SessionResponse
	var production []protocol.App
	revision := 0
	h.control.Handle([]string{toolListSessions}, nil, func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		installationtest.WriteJSON(w, map[string]any{"items": development})
	})
	h.control.Handle([]string{protocol.ToolGetSession, protocol.ToolUpdateSession}, map[string]string{"session": "production"},
		func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			if installationtest.ToolName(r) == protocol.ToolUpdateSession {
				var input protocol.UpdateSessionRequestFor[protocol.AppsRequestFor[protocol.App]]
				if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
					t.Error(err)
				}
				production = input.Desired.Apps
				revision++
			} else if revision == 0 {
				http.NotFound(w, r)
				return
			}
			installationtest.WriteJSON(w, protocol.SessionResponse{
				Name: "production", UID: "production-uid", Apps: production, Generation: int64(revision), Revision: strconv.Itoa(revision),
				Endpoints:  []protocol.SessionEndpoint{{Name: "prod", URL: "https://apps.example/mcp/prod"}},
				Conditions: []protocol.Condition{{Type: protocol.ConditionReady, Status: "True", Reason: "Ready"}},
			})
		})
	builds := 0
	build := buildImage(t, "built")
	h.start(t, func(ctx context.Context, request buildRequest, progress io.Writer) (string, error) {
		mu.Lock()
		builds++
		mu.Unlock()
		return build(ctx, request, progress)
	})
	deploy := func() (string, int) {
		t.Helper()
		if err := executeCommand(t.Context(), []string{"deploy", "--project", filepath.Join(root, manifestFileName)}, io.Discard, io.Discard, h.run); err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		defer mu.Unlock()
		return production[0].Image, builds
	}

	built, count := deploy()
	if count != 1 {
		t.Fatalf("first deploy built %d times", count)
	}
	// A development session now runs the image a build of this source produces.
	mu.Lock()
	development = []protocol.SessionResponse{{Name: "gentle-otter-482", Apps: []protocol.App{{Name: "prod", Image: built}}}}
	mu.Unlock()
	if image, count := deploy(); image != built || count != 1 {
		t.Fatalf("deploy of unchanged source: image %s (want %s), %d builds", image, built, count)
	}
	sourceFiles(t, root, map[string]string{"server.js": "export default {edited: true}"})
	if _, count := deploy(); count != 2 {
		t.Fatalf("deploy of edited source reused an image: %d builds", count)
	}
}
