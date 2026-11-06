package installation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"k8s.io/utils/clock"
	testingclock "k8s.io/utils/clock/testing"

	"github.com/holon-sh/cli/pkg/installation/installationtest"
	"github.com/holon-sh/protocol"
)

func TestDevWatcherReportsWithoutChangeConsumer(t *testing.T) {
	var stream bytes.Buffer
	output, _, _ := newProgressOutput(io.Discard, &stream, false, clock.RealClock{})
	events := make(chan fsnotify.Event)
	watcher := devWatcher{
		watcher: &fsnotify.Watcher{Events: events, Errors: make(chan error)},
		output:  output, done: make(chan struct{}),
	}
	go watcher.run()
	watcher.watcher.Errors <- fsnotify.ErrEventOverflow
	close(events)
	<-watcher.done
	want := "file watcher " + fsnotify.ErrEventOverflow.Error() + "\n"
	if stream.String() != want {
		t.Fatalf("watcher diagnostics = %q, want %q", stream.String(), want)
	}
}

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
		name, done string
		edit       map[string]string
		rebuilt    []string
	}{
		{
			name: "metadata saved unchanged", done: "no app changed",
			edit: map[string]string{manifestFileName: manifest + "# comment\n", "beta/server.json": string(beta)},
		},
		{
			name: "ignored server.json edited", done: "remote image is unchanged", rebuilt: []string{"beta"},
			edit: map[string]string{"beta/server.json": strings.Replace(string(beta), "Example MCP server", "edited", 1)},
		},
		{
			name: "registry selection edited", done: "remote image is unchanged", rebuilt: []string{"gamma"},
			edit: map[string]string{manifestFileName: strings.Replace(manifest, `"version":"1.4.2"`, `"version":"1.4.2","package":{"registryType":"oci"}`, 1)},
		},
		{
			name: "image digest edited", done: "remote update requested", rebuilt: []string{"delta"},
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
			var served []protocol.App
			h.control.Handle([]string{protocol.ToolCreateSession, protocol.ToolGetSession, protocol.ToolRenewSession, protocol.ToolUpdateSession, protocol.ToolDeleteSession},
				nil, func(w http.ResponseWriter, r *http.Request) {
					mu.Lock()
					defer mu.Unlock()
					switch installationtest.ToolName(r) {
					case protocol.ToolDeleteSession:
						installationtest.WriteJSON(w, protocol.DeleteSessionResultFor[protocol.SessionResponse]{Deleted: true})
						return
					case protocol.ToolCreateSession:
						var input protocol.AppsRequestFor[protocol.App]
						if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
							t.Error(err)
						}
						served = input.Apps
					}
					response := apiSession(h, "alpha")
					response["apps"] = served
					installationtest.WriteJSON(w, response)
				})
			h.start(t, buildImage(t, "unchanged"))
			fc := testingclock.NewFakeClock(time.Now())
			h.run.Clock = fc
			stdout, stderr, stop := startDev(t, h.run, "--project", filepath.Join(root, manifestFileName))
			stepUntil(t, fc, devDebounce, func() bool { return strings.Contains(stdout.String(), "Watching for changes.") })
			started := len(stderr.String())
			sourceFiles(t, root, test.edit)
			stepUntil(t, fc, devDebounce, func() bool { return strings.Contains(stderr.String()[started:], test.done) })
			if err := stop(); err != nil {
				t.Fatalf("dev run: %v, stderr=%q", err, stderr.String())
			}
			var rebuilt []string
			for _, match := range regexp.MustCompile(`(?m)^(\S+) (?:building|importing)$`).FindAllStringSubmatch(stderr.String()[started:], -1) {
				rebuilt = append(rebuilt, match[1])
			}
			if !slices.Equal(rebuilt, test.rebuilt) {
				t.Fatalf("rebuilt = %v, want %v, stderr=%q", rebuilt, test.rebuilt, stderr.String()[started:])
			}
		})
	}
}
