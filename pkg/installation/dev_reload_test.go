package installation

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
	testingclock "k8s.io/utils/clock/testing"

	"github.com/holon-sh/cli/pkg/installation/installationtest"
	"github.com/holon-sh/protocol"
)

// reloadFixture runs holon dev for one source app, alpha, against a fake control plane and registry.
// Like the API, it holds a get_session that names the session's current revision until the session moves.
type reloadFixture struct {
	root     string
	registry *imageRegistry
	stdout   *installationtest.ObservedOutput
	stderr   *installationtest.ObservedOutput
	await    func(check func() bool)
	stop     func() error
	baseHost string

	mu       sync.Mutex
	ready    bool
	failNext bool
	revision int
	changed  chan struct{} // closed when the session moves past revision
	reads    []string      // the revision each get_session named
	reloads  []reloadAppRequest
}

// update changes the session and moves it past its revision, which releases a held read.
func (f *reloadFixture) update(change func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	change()
	f.moved()
}

func (f *reloadFixture) moved() {
	f.revision++
	close(f.changed)
	f.changed = make(chan struct{})
}

func (f *reloadFixture) reloadRequests() []reloadAppRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.reloads)
}

// startReloadFixture starts holon dev and, for a ready session, waits until it watches for changes.
func startReloadFixture(t *testing.T, ready bool) *reloadFixture {
	t.Helper()
	f := &reloadFixture{root: t.TempDir(), ready: ready, changed: make(chan struct{})}
	sourceFiles(t, f.root, map[string]string{
		manifestFileName:  "apps:\n- name: alpha\n  access: {server: {}}\n  path: ./alpha\n",
		"alpha/server.js": "export default {}", "alpha/package.json": sourcePackage,
		"alpha/tools.py": "first", "alpha/pyproject.toml": "[project]\nname = \"alpha\"\n", "alpha/ui/main.ts": "export {}",
	})
	if err := os.Symlink("tools.py", filepath.Join(f.root, "alpha", "alias.py")); err != nil {
		t.Fatal(err)
	}
	var registry *httptest.Server
	f.registry, registry = buildRegistry(t)
	h := newRunHarness(t)
	h.control.Handle([]string{protocol.ToolUploadGrant}, nil, func(w http.ResponseWriter, _ *http.Request) {
		installationtest.WriteJSON(w, buildGrant(registry))
	})
	var served []protocol.App
	h.control.Handle([]string{protocol.ToolCreateSession, protocol.ToolGetSession, protocol.ToolRenewSession, protocol.ToolUpdateSession, protocol.ToolDeleteSession},
		nil, func(w http.ResponseWriter, r *http.Request) {
			f.mu.Lock()
			defer f.mu.Unlock()
			var input struct {
				Apps    []protocol.App `json:"apps"`
				Desired struct {
					Apps []protocol.App `json:"apps"`
				} `json:"desired"`
				Revision string `json:"revision"`
			}
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Error(err)
			}
			switch installationtest.ToolName(r) {
			case protocol.ToolDeleteSession:
				installationtest.WriteJSON(w, protocol.DeleteSessionResultFor[protocol.SessionResponse]{Deleted: true})
				return
			case protocol.ToolCreateSession:
				served = input.Apps
			case protocol.ToolUpdateSession:
				served = input.Desired.Apps
				f.moved()
			case protocol.ToolGetSession:
				f.reads = append(f.reads, input.Revision)
				for input.Revision == strconv.Itoa(f.revision) && r.Context().Err() == nil {
					changed := f.changed
					f.mu.Unlock()
					select {
					case <-changed:
					case <-r.Context().Done():
					}
					f.mu.Lock()
				}
			}
			response := apiSession(h, "alpha")
			response["apps"], response["revision"] = served, strconv.Itoa(f.revision)
			if !f.ready {
				// As the API does, publish no endpoints until the apps are up.
				response["endpoints"] = []protocol.SessionEndpoint{}
				response["conditions"] = []protocol.Condition{{Type: protocol.ConditionReady, Status: "False", Reason: "Provisioning"}}
			}
			installationtest.WriteJSON(w, response)
		})
	h.control.Handle([]string{toolReloadApp}, nil, func(w http.ResponseWriter, r *http.Request) {
		var input reloadAppRequest
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		f.reloads = append(f.reloads, input)
		if f.failNext {
			f.failNext = false
			w.WriteHeader(http.StatusConflict)
			return
		}
		installationtest.WriteJSON(w, reloadAppResult{Digest: input.Digest, ElapsedSeconds: 1.2})
	})
	h.start(t, buildImage(t, "built"))
	f.baseHost = h.baseHost
	fc := testingclock.NewFakeClock(time.Now())
	h.run.Clock = fc
	var stop func() error
	f.stdout, f.stderr, stop = startDev(t, h.run, "--project", filepath.Join(f.root, manifestFileName))
	f.stop = sync.OnceValue(stop)
	t.Cleanup(func() { _ = f.stop() })
	f.await = func(check func() bool) {
		t.Helper()
		stepUntil(t, fc, devDebounce, check)
	}
	if ready {
		f.await(func() bool { return strings.Contains(f.stdout.String(), "Watching for changes.") })
	}
	return f
}

func TestDevSaves(t *testing.T) {
	const reloaded = "alpha reloaded in 1.2 s"
	for _, test := range []struct {
		name       string
		failReload bool
		saves      []map[string]string // each awaits the line at the same index of lines
		lines      []string
		reloads    int
	}{
		{
			name:  "an unchanged snapshot sends nothing",
			saves: []map[string]string{{"alpha/tools.py": "second"}, {"alpha/tools.py": "second"}}, lines: []string{reloaded, "alpha unchanged"}, reloads: 1,
		},
		{
			name:  "files an editor writes while saving do nothing",
			saves: []map[string]string{{"alpha/.tools.py.swp": "swap", "alpha/tools.py~": "first", "alpha/tools.py": "second"}}, lines: []string{reloaded}, reloads: 1,
		},
		{
			name:  "a module under ui/ rebuilds, since the frontend build reads it",
			saves: []map[string]string{{"alpha/ui/main.ts": "export const edited = true"}}, lines: []string{"alpha building"},
		},
		{
			name:  "a dependency manifest rebuilds",
			saves: []map[string]string{{"alpha/pyproject.toml": "[project]\nname = \"renamed\"\n"}}, lines: []string{"alpha building"},
		},
		{
			name: "a failed reload rebuilds", failReload: true,
			saves: []map[string]string{{"alpha/tools.py": "second"}}, lines: []string{"alpha building"}, reloads: 1,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := startReloadFixture(t, true)
			f.update(func() { f.failNext = test.failReload })
			for i, files := range test.saves {
				from := len(f.stderr.String())
				sourceFiles(t, f.root, files)
				f.await(func() bool { return strings.Contains(f.stderr.String()[from:], test.lines[i]) })
			}
			if requests := f.reloadRequests(); len(requests) != test.reloads {
				t.Fatalf("reloads = %d; stderr=%q", len(requests), f.stderr.String())
			}
		})
	}
}

func TestDevReloadPushesTheBuildSnapshot(t *testing.T) {
	f := startReloadFixture(t, true)
	started := len(f.stderr.String())
	sourceFiles(t, f.root, map[string]string{"alpha/tools.py": "second"})
	f.await(func() bool { return strings.Contains(f.stderr.String()[started:], "alpha reloaded") })
	request := f.reloadRequests()[0]
	if request.Session != "gentle-otter-482" || request.App != "alpha" || request.MediaType != overlayMediaType {
		t.Fatalf("reload_app %+v", request)
	}
	f.registry.mu.Lock()
	blob := f.registry.items["holon/upload/alpha/blobs/"+request.Digest].data
	f.registry.mu.Unlock()
	decoder, err := zstd.NewReader(bytes.NewReader(blob))
	if err != nil {
		t.Fatal(err)
	}
	defer decoder.Close()
	entries := map[string]string{}
	for archive := tar.NewReader(decoder); ; {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(archive)
		entries[header.Name] = string(data) + header.Linkname
	}
	// The snapshot an image build would read, with its launcher.
	if entries["tools.py"] != "second" || entries["alias.py"] != "tools.py" || !strings.Contains(entries[".holon/main.mjs"], "../server.js") {
		t.Fatalf("overlay entries = %v", entries)
	}
}

func TestDevStartup(t *testing.T) {
	f := startReloadFixture(t, false)
	endpoints := "  MCP tools (all apps)  https://gentle-otter-482." + f.baseHost + "/mcp\n\n" +
		"  alpha\n    Browser  https://gentle-otter-482." + f.baseHost + "/alpha/\n" +
		"    MCP      https://gentle-otter-482." + f.baseHost + "/mcp/alpha\n\n"
	starting := "Session  gentle-otter-482\n\nStarting\n" + endpoints
	f.await(func() bool { return f.stdout.String() == starting })
	f.update(func() { f.ready = true })
	f.await(func() bool { return strings.Contains(f.stdout.String(), "Watching for changes.") })
	if want := starting + "Ready\n" + "Watching for changes. Ctrl-C stops this session.\n"; f.stdout.String() != want {
		t.Fatalf("stdout = %q, want %q", f.stdout.String(), want)
	}
	// Each read names the revision the session last had, so the server holds it until the session moves.
	f.await(func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return slices.Equal(f.reads, []string{"0", "1"})
	})
}

// A save while the last save's types are still being prepared reloads at once, and stops that preparation.
func TestDevSaveStopsTheLastPreparation(t *testing.T) {
	bin, started := t.TempDir(), filepath.Join(t.TempDir(), "started")
	// Stands in for npm installing the missing SDK runtime, which lasts until it is stopped.
	sourceFiles(t, bin, map[string]string{"npm": "#!/bin/sh\necho $$ >> '" + started + "'\nexec sleep 600\n"})
	if err := os.Chmod(filepath.Join(bin, "npm"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	f := startReloadFixture(t, true)
	alpha := filepath.Join(f.root, "alpha")
	installRuntime(t, alpha)
	from := len(f.stderr.String())
	sourceFiles(t, f.root, map[string]string{"alpha/package.json": `{"private":true,"dependencies":{"holon-sh":"0.0.1"}}`})
	f.await(func() bool { return strings.Contains(f.stderr.String()[from:], "alpha ready") })
	if err := os.RemoveAll(filepath.Join(alpha, "node_modules", nodeRuntime)); err != nil {
		t.Fatal(err)
	}
	runs := func() []string {
		data, _ := os.ReadFile(started)
		return strings.Fields(string(data))
	}
	for i, content := range []string{"second", "third"} {
		from := len(f.stderr.String())
		sourceFiles(t, f.root, map[string]string{"alpha/tools.py": content})
		f.await(func() bool { return strings.Contains(f.stderr.String()[from:], "alpha reloaded") && len(runs()) == i+1 })
	}
	pid, err := strconv.Atoi(runs()[0])
	if err != nil {
		t.Fatal(err)
	}
	if process, _ := os.FindProcess(pid); process.Signal(syscall.Signal(0)) == nil {
		t.Fatal("the first save's preparation still runs")
	}
	if err := f.stop(); err != nil || strings.Contains(f.stderr.String(), "MCP types") {
		t.Fatalf("run: %v stderr=%q", err, f.stderr.String())
	}
}
