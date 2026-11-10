package installation

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/holon-sh/cli/pkg/installation/installationtest"
	"github.com/holon-sh/protocol"
)

func TestDetachedDevLifecycle(t *testing.T) {
	for _, tc := range []struct {
		name      string
		flags     []string
		seconds   int64
		wantError string
	}{
		{name: "ready", seconds: 86400},
		{name: "JSON", flags: []string{"--json", "--ttl", "1s"}, seconds: 1},
		{name: "failed hook", seconds: 86400, wantError: "install hook failed"},
		{name: "cancelled startup", seconds: 86400, wantError: "context canceled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			sourceFiles(t, root, map[string]string{manifestFileName: `apps:
  - name: echo
    remote: {url: https://upstream.example/mcp}
    access: {server: {}}
`})
			h := newRunHarness(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var deleted atomic.Bool
			created := protocol.SessionResponse{
				Name: "gentle-otter-482", UID: "preview-uid", Revision: "1", Generation: 1, ExpiresAt: "2030-01-02T00:00:00Z",
			}
			h.control.Handle([]string{protocol.ToolCreateSession}, nil, func(w http.ResponseWriter, r *http.Request) {
				installationtest.AssertOrigin(t, r, "dev."+h.baseHost)
				var request protocol.AppsRequestFor[protocol.App]
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.TTLSeconds == nil || *request.TTLSeconds != tc.seconds {
					t.Errorf("detached request: %#v, %v", request, err)
				}
				created.Apps = request.Apps
				installationtest.WriteJSON(w, created)
			})
			h.control.Handle([]string{protocol.ToolGetSession}, map[string]string{"session": "gentle-otter-482"}, func(w http.ResponseWriter, _ *http.Request) {
				ready := created
				ready.Endpoints = []protocol.SessionEndpoint{{Name: "echo", URL: "https://preview.example/mcp/echo"}}
				ready.Conditions = []protocol.Condition{{Type: protocol.ConditionReady, Status: "True"}}
				switch tc.name {
				case "cancelled startup":
					cancel()
					ready.Conditions = nil
				case "failed hook":
					ready.Operation = &protocol.LifecycleOperation{ID: "install"}
					ready.Lifecycle = &protocol.LifecycleStatus{ID: "install", Phase: "Failed", Message: "install hook failed"}
				}
				installationtest.WriteJSON(w, ready)
			})
			h.control.Handle([]string{protocol.ToolDeleteSession}, map[string]string{"session": "gentle-otter-482"}, func(w http.ResponseWriter, r *http.Request) {
				if installationtest.ToolArguments(r)["skipHooks"] != true {
					t.Error("cleanup must bypass lifecycle hooks")
				}
				if installationtest.ToolArguments(r)["expectedRevision"] != "1" {
					t.Error("cleanup missing current revision")
				}
				deleted.Store(true)
				installationtest.WriteJSON(w, protocol.DeleteSessionResultFor[protocol.SessionResponse]{Deleted: true})
			})
			h.start(t, nil)
			args := []string{"dev", "-p", filepath.Join(root, manifestFileName), "--detach"}
			args = append(args, tc.flags...)
			var output bytes.Buffer
			err := executeCommand(ctx, args, &output, nil, h.run)
			switch {
			case tc.wantError != "":
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("failed startup = %v", err)
				}
			case tc.name == "JSON":
				var result protocol.SessionResponse
				if err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(output.Bytes(), &result); err != nil {
					t.Fatalf("stdout must contain only JSON: %v, output=%s", err, output.String())
				}
				if result.Name != created.Name || result.ExpiresAt != created.ExpiresAt ||
					len(result.Endpoints) != 1 || result.Endpoints[0].URL != "https://preview.example/mcp/echo" {
					t.Fatalf("detached JSON = %+v", result)
				}
			default:
				if err != nil || !strings.Contains(output.String(), "https://gentle-otter-482."+h.baseHost+"/mcp/echo") ||
					!strings.Contains(output.String(), "Expires  2030-01-02T00:00:00Z") {
					t.Fatalf("detached session = %v, output=%s", err, output.String())
				}
			}
			if deleted.Load() != (tc.wantError != "") {
				t.Fatalf("session cleanup: deleted=%v, error=%v", deleted.Load(), err)
			}
		})
	}
}
