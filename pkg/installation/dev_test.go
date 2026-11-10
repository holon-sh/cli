package installation

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	testingclock "k8s.io/utils/clock/testing"

	"github.com/holon-sh/cli/pkg/installation/installationtest"
	"github.com/holon-sh/protocol"
)

func TestRunDevRebuildsWhatASaveChanged(t *testing.T) {
	fixture, registry := buildRegistry(t)
	grant := buildGrant(registry)
	grant.Repositories = map[string]string{
		"alpha": "holon/upload/alpha", "beta": "holon/upload/beta", "gamma": "holon/upload/gamma", "delta": "holon/upload/delta",
	}
	first := addTestImage(t, fixture, "amd64", ocispec.ImageConfig{}, nil).Digest.String()
	second := addTestImage(t, fixture, "arm64", ocispec.ImageConfig{}, nil).Digest.String()
	fixture.items["source/manifests/1.0.0"] = fixture.items["source/manifests/"+first]
	gamma := descriptorDocument("gamma")
	gamma["packages"].([]any)[0].(map[string]any)["identifier"] = grant.Registry + "/source:1.0.0"
	gammaSource, _ := json.Marshal(descriptorRegistry(t, gamma))
	manifest := fmt.Sprintf("apps:\n- name: alpha\n  access: {server: {}}\n  path: ./alpha\n"+
		"- name: beta\n  access: {server: {}}\n  path: ./beta\n- name: gamma\n  access: {server: {}}\n  registry: %s\n"+
		"- name: delta\n  access: {server: {}}\n  image: %s/source@%s\n", gammaSource, grant.Registry, first)
	beta, _ := json.Marshal(descriptorDocument("beta"))
	for _, test := range []struct {
		name     string
		edit     map[string]string
		replaced []string
	}{
		{
			name: "ignored server.json edited",
			edit: map[string]string{"beta/server.json": strings.Replace(string(beta), "Example MCP server", "edited", 1)},
		},
		{
			name: "registry selection edited",
			edit: map[string]string{manifestFileName: strings.Replace(manifest, `"version":"1.4.2"`, `"version":"1.4.2","package":{"registryType":"oci"}`, 1)},
		},
		{
			name: "image digest edited", replaced: []string{"delta"},
			edit: map[string]string{manifestFileName: strings.Replace(manifest, first, second, 1)},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			sourceFiles(t, root, map[string]string{
				manifestFileName:  manifest,
				"alpha/server.js": "export default {}", "alpha/package.json": sourcePackage,
				"beta/server.js": "export default {}", "beta/package.json": sourcePackage, "beta/server.json": string(beta),
				"beta/" + dockerignoreFile: "server.json\n",
			})
			h := newRunHarness(t)
			h.control.Handle([]string{protocol.ToolUploadGrant}, nil, func(w http.ResponseWriter, _ *http.Request) {
				installationtest.WriteJSON(w, grant)
			})
			var mu sync.Mutex
			var created, served []protocol.App
			h.control.Handle([]string{protocol.ToolCreateSession, protocol.ToolGetSession, protocol.ToolRenewSession, protocol.ToolUpdateSession},
				nil, func(w http.ResponseWriter, r *http.Request) {
					mu.Lock()
					defer mu.Unlock()
					switch installationtest.ToolName(r) {
					case protocol.ToolCreateSession:
						var input protocol.AppsRequestFor[protocol.App]
						if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
							t.Error(err)
						}
						created, served = input.Apps, input.Apps
					case protocol.ToolUpdateSession:
						var input protocol.UpdateSessionRequestFor[protocol.AppsRequestFor[protocol.App]]
						if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
							t.Error(err)
						}
						served = input.Desired.Apps
					}
					response := apiSession("alpha")
					response["apps"] = served
					installationtest.WriteJSON(w, response)
				})
			h.start(t, buildImage(t, "unchanged"))
			fc := testingclock.NewFakeClock(time.Now())
			h.run.Clock = fc
			_, stderr, stop := startDev(t, h.run, "--project", filepath.Join(root, manifestFileName))
			stepUntil(t, fc, devDebounce, func() bool { return strings.Contains(stderr.String(), watching) })
			started := len(stderr.String())
			sourceFiles(t, root, test.edit)
			stepUntil(t, fc, devDebounce, func() bool { return strings.Contains(stderr.String()[started:], watching) })
			if err := stop(); err != nil {
				t.Fatalf("dev run: %v, stderr=%q", err, stderr.String())
			}
			// A project change builds no source app whose image inputs it left alone.
			if strings.Contains(stderr.String()[started:], "building") {
				t.Fatalf("stderr=%q", stderr.String()[started:])
			}
			mu.Lock()
			defer mu.Unlock()
			var replaced []string
			for i, app := range served {
				if app.Image != created[i].Image {
					replaced = append(replaced, app.Name)
				}
			}
			if !slices.Equal(replaced, test.replaced) {
				t.Fatalf("replaced images = %v, want %v, stderr=%q", replaced, test.replaced, stderr.String()[started:])
			}
		})
	}
}
