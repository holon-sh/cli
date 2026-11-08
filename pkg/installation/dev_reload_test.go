package installation

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
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
	start    func() // runs holon dev again, as a later command would
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
		manifestFileName:  "apps:\n- name: alpha\n  access: {server: {}}\n  path: ./alpha\n",
		"alpha/server.js": "export default {}", "alpha/package.json": sourcePackage,
		"alpha/tools.py": "first", "alpha/pyproject.toml": "[project]\nname = \"alpha\"\n", "alpha/ui/main.ts": "export {}",
		"alpha/.holon-types/notes.txt": "only image builds read this",
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
	h.control.Handle([]string{toolListSessions}, nil, func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		items := []map[string]any{}
		if f.served != nil {
			items = append(items, session())
		}
		installationtest.WriteJSON(w, map[string]any{"items": items})
	})
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
		installationtest.WriteJSON(w, reloadAppResult{Digest: input.Digest, ElapsedSeconds: 1.2})
	})
	h.start(t, buildImage(t, "built"))
	f.baseHost = h.baseHost
	fc := testingclock.NewFakeClock(time.Now())
	h.run.Clock = fc
	f.await = func(check func() bool) {
		t.Helper()
		stepUntil(t, fc, devDebounce, check)
	}
	f.start = func() {
		t.Helper()
		var stop func() error
		f.stdout, f.stderr, stop = startDev(t, h.run, "--project", filepath.Join(f.root, manifestFileName))
		stopOnce := sync.OnceValue(stop)
		f.stop = stopOnce
		t.Cleanup(func() { _ = stopOnce() })
		if f.ready {
			f.await(func() bool { return strings.Contains(f.stdout.String(), "Watching for changes.") })
		}
	}
	f.start()
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
		rebuilds   bool
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
			saves: []map[string]string{{"alpha/ui/main.ts": "export const edited = true"}}, lines: []string{"alpha building"}, rebuilds: true,
		},
		{
			name:  "a dependency manifest rebuilds",
			saves: []map[string]string{{"alpha/pyproject.toml": "[project]\nname = \"renamed\"\n"}}, lines: []string{"alpha building"}, rebuilds: true,
		},
		{
			name: "a failed reload rebuilds", failReload: true,
			saves: []map[string]string{{"alpha/tools.py": "second"}}, lines: []string{"alpha reload failed"}, reloads: 1, rebuilds: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := startReloadFixture(t, true)
			f.update(func() { f.failNext = test.failReload })
			started := len(f.stderr.String())
			for i, files := range test.saves {
				from := len(f.stderr.String())
				sourceFiles(t, f.root, files)
				f.await(func() bool { return strings.Contains(f.stderr.String()[from:], test.lines[i]) })
			}
			if test.rebuilds {
				f.await(func() bool { return strings.Contains(f.stderr.String()[started:], "app rebuild") })
			}
			output := f.stderr.String()[started:]
			if requests := f.reloadRequests(); len(requests) != test.reloads || strings.Contains(output, "alpha building") != test.rebuilds {
				t.Fatalf("reloads = %d, rebuilt = %v; stderr=%q", len(requests), strings.Contains(output, "alpha building"), output)
			}
			if err := f.stop(); err != nil {
				t.Fatalf("run: %v stderr=%q", err, f.stderr.String())
			}
		})
	}
}

func TestDevPicksUpTheSessionAnEarlierRunLeft(t *testing.T) {
	for _, test := range []struct {
		name    string
		between func(f *reloadFixture) // changes the session while no holon dev runs
		edit    map[string]string      // made while no holon dev runs
		creates int
		reloads int
		rebuilt bool
	}{
		{name: "server source edited since reloads into the kept image", edit: map[string]string{"alpha/tools.py": "second"}, creates: 1, reloads: 1},
		{
			name: "a dependency manifest edited since rebuilds", edit: map[string]string{"alpha/pyproject.toml": "[project]\nname = \"renamed\"\n"},
			creates: 1, rebuilt: true,
		},
		{name: "another caller's session is not picked up", between: func(f *reloadFixture) { f.foreign = true }, creates: 2},
		{
			name: "a session running other apps is not picked up", creates: 2,
			between: func(f *reloadFixture) { f.served = append(f.served, protocol.App{Name: "beta"}) },
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := startReloadFixture(t, true)
			if err := f.stop(); err != nil {
				t.Fatalf("first run: %v stderr=%q", err, f.stderr.String())
			}
			var before string
			f.update(func() {
				before = f.served[0].Image
				if test.between != nil {
					test.between(f)
				}
			})
			sourceFiles(t, f.root, test.edit)
			f.start()
			f.mu.Lock()
			creates, image := f.creates, f.served[0].Image
			f.mu.Unlock()
			if creates != test.creates || !strings.HasPrefix(f.stdout.String(), "Session  gentle-otter-482\n\nStarting\n") {
				t.Fatalf("creates = %d, want %d; stdout=%q stderr=%q", creates, test.creates, f.stdout.String(), f.stderr.String())
			}
			if test.between == nil && (len(f.reloadRequests()) != test.reloads || (image != before) != test.rebuilt) {
				t.Fatalf("reloads = %d, image replaced = %v; stderr=%q", len(f.reloadRequests()), image != before, f.stderr.String())
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
	if request.Session != "gentle-otter-482" || request.App != "alpha" || request.MediaType != overlayMediaType ||
		strings.Contains(f.stderr.String()[started:], "alpha building") {
		t.Fatalf("reload_app %+v; stderr=%q", request, f.stderr.String()[started:])
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
		// Without host times or owners, an unchanged snapshot archives to the same digest.
		if !header.ModTime.Equal(time.Unix(0, 0)) || header.Uid != 0 || header.Uname != "" {
			t.Errorf("%s keeps host metadata", header.Name)
		}
		data, _ := io.ReadAll(archive)
		entries[header.Name] = string(data) + header.Linkname
	}
	// The snapshot an image build would read, with its launcher, without what only image builds read.
	if entries["tools.py"] != "second" || entries["alias.py"] != "tools.py" || !strings.Contains(entries[".holon/main.mjs"], "../server.js") ||
		slices.ContainsFunc(slices.Collect(maps.Keys(entries)), func(name string) bool { return strings.HasPrefix(name, ".holon-types") }) {
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
