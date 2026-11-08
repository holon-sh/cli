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
	"reflect"
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

const (
	alphaProject  = "[project]\nname = \"alpha\"\nversion = \"1\"\n"
	alphaManifest = "apps:\n- name: alpha\n  access: {server: {}}\n  path: ./alpha\n"
)

// reloadFixture runs holon dev for one source app, alpha, against a fake control plane and registry.
// Like the API, it holds a get_session that names the session's current revision until the session moves.
type reloadFixture struct {
	root     string
	registry *imageRegistry
	stdout   *installationtest.ObservedOutput
	stderr   *installationtest.ObservedOutput
	await    func(check func() bool)
	start    func(args ...string) // runs holon dev again, as a later command would
	stop     func() error
	baseHost string

	mu       sync.Mutex
	ready    bool
	failNext bool
	foreign  bool // the session is someone else's, so renewing it fails
	creates  int
	served   []protocol.App
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
		manifestFileName:  alphaManifest,
		"alpha/server.py": "app = None", "alpha/tools.py": "first", "alpha/pyproject.toml": alphaProject, "alpha/ui/main.ts": "export {}",
	})
	var registry *httptest.Server
	f.registry, registry = buildRegistry(t)
	h := newRunHarness(t)
	h.control.Handle([]string{protocol.ToolUploadGrant}, nil, func(w http.ResponseWriter, _ *http.Request) {
		installationtest.WriteJSON(w, buildGrant(registry))
	})
	session := func() map[string]any {
		response := apiSession(h, "alpha")
		response["apps"], response["revision"] = f.served, strconv.Itoa(f.revision)
		if !f.ready {
			// As the API does, publish no endpoints until the apps are up.
			response["endpoints"] = []protocol.SessionEndpoint{}
			response["conditions"] = []protocol.Condition{{Type: protocol.ConditionReady, Status: "False", Reason: "Provisioning"}}
		}
		return response
	}
	h.control.Handle([]string{protocol.ToolCreateSession, protocol.ToolGetSession, protocol.ToolRenewSession, protocol.ToolUpdateSession},
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
			case protocol.ToolCreateSession:
				f.creates++
				f.served = input.Apps
			case protocol.ToolRenewSession:
				if f.foreign {
					http.NotFound(w, r)
					return
				}
			case protocol.ToolUpdateSession:
				f.served = input.Desired.Apps
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
			installationtest.WriteJSON(w, session())
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
		installationtest.WriteJSON(w, reloadAppResult{ElapsedSeconds: 1.2})
	})
	h.start(t, buildImage(t, "built"))
	f.baseHost = h.baseHost
	fc := testingclock.NewFakeClock(time.Now())
	h.run.Clock = fc
	f.await = func(check func() bool) {
		t.Helper()
		stepUntil(t, fc, devDebounce, check)
	}
	f.start = func(args ...string) {
		t.Helper()
		var stop func() error
		f.stdout, f.stderr, stop = startDev(t, h.run, append([]string{"--project", filepath.Join(f.root, manifestFileName)}, args...)...)
		stopOnce := sync.OnceValue(stop)
		f.stop = stopOnce
		t.Cleanup(func() { _ = stopOnce() })
	}
	f.start()
	if ready {
		f.watching()
	}
	return f
}

func (f *reloadFixture) watching() {
	f.await(func() bool { return strings.Contains(f.stdout.String(), "Watching for changes.") })
}

func (f *reloadFixture) created() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.creates
}

func TestDevSaves(t *testing.T) {
	const reloaded = "alpha reloaded in 1.2 s"
	for _, test := range []struct {
		name       string
		failReload bool
		saves      []map[string]string // each awaits the line at the same index of lines
		lines      []string
		reloads    int
		rebuilt    bool
	}{
		{
			name:  "an unchanged snapshot sends nothing",
			saves: []map[string]string{{"alpha/tools.py": "second"}, {"alpha/tools.py": "second"}}, lines: []string{reloaded, "alpha unchanged"}, reloads: 1,
		},
		{
			name:  "files an editor writes while saving do nothing",
			saves: []map[string]string{{"alpha/.tools.py.swp": "swap", "alpha/tools.py~": "first", manifestFileName: alphaManifest + "# comment\n"}},
			lines: []string{"no app changed"},
		},
		{
			name:  "a module under ui/ reloads and rebuilds, since the frontend build reads it",
			saves: []map[string]string{{"alpha/ui/main.ts": "export const edited = true"}}, lines: []string{"remote update requested"}, reloads: 1, rebuilt: true,
		},
		{
			name: "a failed reload is reported and rebuilds nothing", failReload: true,
			saves: []map[string]string{{"alpha/tools.py": "second"}}, lines: []string{"alpha reload failed"}, reloads: 1,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := startReloadFixture(t, true)
			var before protocol.App
			f.update(func() { f.failNext, before = test.failReload, f.served[0] })
			for i, files := range test.saves {
				from := len(f.stderr.String())
				sourceFiles(t, f.root, files)
				f.await(func() bool { return strings.Contains(f.stderr.String()[from:], test.lines[i]) })
			}
			requests := f.reloadRequests()
			if len(requests) != test.reloads {
				t.Fatalf("reloads = %d; stderr=%q", len(requests), f.stderr.String())
			}
			f.mu.Lock()
			after := f.served[0]
			f.mu.Unlock()
			if rebuilt := after.Image != before.Image; rebuilt != test.rebuilt {
				t.Fatalf("image replaced = %v; stderr=%q", rebuilt, f.stderr.String())
			}
			// The update that replaces the image carries the overlay of what was saved.
			if test.rebuilt && after.Overlay != requests[len(requests)-1].Digest {
				t.Fatalf("rebuilt with overlay %+v after reloading %+v", after.Overlay, requests)
			}
		})
	}
}

func TestDevPicksUpTheSessionAnEarlierRunLeft(t *testing.T) {
	for _, test := range []struct {
		name    string
		between func(t *testing.T, f *reloadFixture) // changes things while no holon dev runs
		edit    map[string]string                    // made while no holon dev runs
		creates int
		reloads int
		rebuilt bool
	}{
		{name: "server source edited since reloads into the kept image", edit: map[string]string{"alpha/tools.py": "second"}, creates: 1, reloads: 1},
		{
			name: "a dependency manifest edited since rebuilds", edit: map[string]string{"alpha/pyproject.toml": alphaProject + "dependencies = [\"idna\"]\n"},
			creates: 1, rebuilt: true,
		},
		{name: "another caller's session is not picked up", between: func(_ *testing.T, f *reloadFixture) { f.foreign = true }, creates: 2},
		{
			name: "the same apps from another directory start their own session", creates: 2,
			between: func(t *testing.T, f *reloadFixture) {
				moved := filepath.Join(t.TempDir(), "elsewhere")
				if err := os.Rename(f.root, moved); err != nil {
					t.Fatal(err)
				}
				f.root = moved
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := startReloadFixture(t, true)
			if err := f.stop(); err != nil {
				t.Fatalf("first run: %v stderr=%q", err, f.stderr.String())
			}
			var before protocol.App
			f.update(func() {
				before = f.served[0]
				if test.between != nil {
					test.between(t, f)
				}
			})
			sourceFiles(t, f.root, test.edit)
			f.start()
			f.watching()
			f.mu.Lock()
			after := f.served[0]
			f.mu.Unlock()
			if f.created() != test.creates || !strings.HasPrefix(f.stdout.String(), "Session  gentle-otter-482\n\nStarting\n") {
				t.Fatalf("creates = %d, want %d; stdout=%q stderr=%q", f.created(), test.creates, f.stdout.String(), f.stderr.String())
			}
			if rebuilt := !reflect.DeepEqual(after, before); len(f.reloadRequests()) != test.reloads || rebuilt != test.rebuilt {
				t.Fatalf("reloads = %d, app replaced = %v; stderr=%q", len(f.reloadRequests()), rebuilt, f.stderr.String())
			}
		})
	}
}

func TestDevDetachedStartsItsOwnSession(t *testing.T) {
	f := startReloadFixture(t, true)
	if err := f.stop(); err != nil {
		t.Fatalf("first run: %v stderr=%q", err, f.stderr.String())
	}
	f.start("--detach")
	f.await(func() bool { return f.created() == 2 })
	f.update(func() {}) // releases the readiness read, which the fixture holds at the session's current revision
	f.await(func() bool { return strings.Contains(f.stdout.String(), "Expires") })
}

func TestDevReloadPushesTheBuildSnapshot(t *testing.T) {
	f := startReloadFixture(t, true)
	if err := os.Symlink("tools.py", filepath.Join(f.root, "alpha", "alias.py")); err != nil {
		t.Fatal(err)
	}
	started := len(f.stderr.String())
	sourceFiles(t, f.root, map[string]string{"alpha/tools.py": "second"})
	f.await(func() bool { return strings.Contains(f.stderr.String()[started:], "alpha reloaded") })
	request := f.reloadRequests()[0]
	if request.Session != "gentle-otter-482" || request.App != "alpha" {
		t.Fatalf("reload_app %+v", request)
	}
	f.mu.Lock()
	first := f.served[0].Overlay
	f.mu.Unlock()
	if first == "" || first == request.Digest {
		t.Fatalf("create_session overlay %q, reloaded %s", first, request.Digest)
	}
	if !strings.Contains(string(f.overlay(t, first)["server.py"]), "app = None") {
		t.Fatal("the overlay a session starts from lacks the server source")
	}
	entries := f.overlay(t, request.Digest)
	// The snapshot an image build would read, with its launcher.
	if string(entries["tools.py"]) != "second" || string(entries["alias.py"]) != "tools.py" || !strings.Contains(string(entries[".holon/main.py"]), `start("server")`) {
		t.Fatalf("overlay entries = %v", entries)
	}
}

// overlay returns the entries of the overlay blob with digest, by name; a symlink holds its target.
func (f *reloadFixture) overlay(t *testing.T, digest string) map[string][]byte {
	t.Helper()
	f.registry.mu.Lock()
	blob := f.registry.items["holon/upload/alpha/blobs/"+digest].data
	f.registry.mu.Unlock()
	decoder, err := zstd.NewReader(bytes.NewReader(blob))
	if err != nil {
		t.Fatal(err)
	}
	defer decoder.Close()
	entries := map[string][]byte{}
	for archive := tar.NewReader(decoder); ; {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			return entries
		}
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(archive)
		entries[header.Name] = append(data, header.Linkname...)
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
	if want := starting + "\nReady\n" + endpoints + "Watching for changes. Ctrl-C exits and leaves this session for the next holon dev.\n"; f.stdout.String() != want {
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
	if strings.Contains(f.stderr.String(), "MCP types") {
		t.Fatalf("stderr=%q", f.stderr.String())
	}
}
