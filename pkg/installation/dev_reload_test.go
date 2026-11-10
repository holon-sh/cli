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

const (
	alphaProject  = "[project]\nname = \"alpha\"\nversion = \"1\"\n"
	alphaManifest = "apps:\n- name: alpha\n  access: {server: {}}\n  path: ./alpha\n"
	betaRemote    = "- name: beta\n  access: {server: {}}\n  remote: {url: https://upstream.example/mcp}\n"
	alphaLock     = "version = 1\n\n[[package]]\nname = \"alpha\"\nversion = \"1\"\nsource = { virtual = \".\" }\n"
)

// reloadFixture runs holon dev for one source app, alpha, against a fake control plane and registry.
// Like the API, it holds a get_session that names the session's current revision until the session moves.
type reloadFixture struct {
	t        *testing.T
	root     string
	registry *imageRegistry
	stdout   *installationtest.ObservedOutput
	stderr   *installationtest.ObservedOutput
	await    func(check func() bool)
	start    func(args ...string)
	stop     func() error
	baseHost string

	mu       sync.Mutex
	ready    bool
	failNext bool // fails the next reload
	failSave bool // fails the next session update
	foreign  bool
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
	f := &reloadFixture{t: t, root: t.TempDir(), ready: ready, changed: make(chan struct{})}
	sourceFiles(t, f.root, map[string]string{
		manifestFileName:  alphaManifest,
		"alpha/server.py": "app = None", "alpha/tools.py": "first", "alpha/pyproject.toml": alphaProject, "alpha/uv.lock": alphaLock,
		"alpha/ui/main.ts": "export {}",
	})
	var registry *httptest.Server
	f.registry, registry = buildRegistry(t)
	h := newRunHarness(t)
	h.control.Handle([]string{protocol.ToolUploadGrant}, nil, func(w http.ResponseWriter, _ *http.Request) {
		grant := buildGrant(registry)
		grant.Repositories["beta"] = "holon/upload/beta"
		installationtest.WriteJSON(w, grant)
	})
	session := func() map[string]any {
		response := apiSession("alpha")
		response["apps"], response["revision"] = f.served, strconv.Itoa(f.revision)
		if !f.ready {
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
				Remove   []string `json:"remove"`
				Revision string   `json:"revision"`
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
				if f.failSave {
					f.failSave = false
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				// Like the API, an update upserts the apps it names.
				f.served = slices.DeleteFunc(f.served, func(app protocol.App) bool {
					return slices.Contains(input.Remove, app.Name) ||
						slices.ContainsFunc(input.Desired.Apps, func(desired protocol.App) bool { return desired.Name == app.Name })
				})
				f.served = append(input.Desired.Apps, f.served...)
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
		// Like the API, a reload records the overlay as the app's.
		f.served[slices.IndexFunc(f.served, func(app protocol.App) bool { return app.Name == input.App })].Overlay = input.Digest
		f.moved()
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
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("stderr=%q", f.stderr.String())
		}
	})
	if ready {
		f.watching()
	}
	return f
}

func (f *reloadFixture) watching() {
	f.await(func() bool { return strings.Contains(f.stderr.String(), watching) })
}

// save writes files and waits until holon dev has handled them and printed output after them.
func (f *reloadFixture) save(files map[string]string, output string) {
	f.t.Helper()
	from := len(f.stderr.String())
	f.mu.Lock()
	reads := len(f.reads)
	f.mu.Unlock()
	sourceFiles(f.t, f.root, files)
	// Each change ends the session read the loop is waiting on, and the loop reads again once it handled it.
	f.await(func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return len(f.reads) > reads && f.stderr.String()[from:] == output
	})
}

func (f *reloadFixture) apps() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var names []string
	for _, app := range f.served {
		names = append(names, app.Name)
	}
	slices.Sort(names)
	return names
}

func (f *reloadFixture) created() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.creates
}

// Each save has one outcome, and what holon dev prints after it says which.
func TestDevSaves(t *testing.T) {
	const rebuilt = "Preparing apps\nalpha building\nalpha uploaded\nDeploying apps\nalpha deploying\nalpha ready\n" + watching + "\n"
	for _, test := range []struct {
		name       string
		failReload bool
		saves      []map[string]string // each prints the output at the same index
		output     []string
		reloads    int
		rebuilt    bool
	}{
		{
			name:   "server source restarts the app in place, and the same source again does nothing",
			saves:  []map[string]string{{"alpha/tools.py": "second"}, {"alpha/tools.py": "second"}},
			output: []string{"alpha reloading\nalpha reloaded in 1.2 s\n", "alpha unchanged\n"}, reloads: 1,
		},
		{
			name:   "files an editor writes while saving do nothing",
			saves:  []map[string]string{{"alpha/.tools.py.swp": "swap", "alpha/tools.py~": "first", manifestFileName: alphaManifest + "# comment\n"}},
			output: []string{""},
		},
		{
			name:   "a module under ui/ rebuilds without reloading, since the frontend build reads it",
			saves:  []map[string]string{{"alpha/ui/main.ts": "export const edited = true"}},
			output: []string{rebuilt}, rebuilt: true,
		},
		{
			name:   "a dependency rebuilds without reloading",
			saves:  []map[string]string{{"alpha/pyproject.toml": alphaProject + "dependencies = [\"idna\"]\n"}},
			output: []string{rebuilt}, rebuilt: true,
		},
		{
			name: "a failed reload is reported and rebuilds nothing", failReload: true,
			saves:  []map[string]string{{"alpha/tools.py": "second"}},
			output: []string{"alpha reloading\nalpha reload failed: Conflict\n"}, reloads: 1,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := startReloadFixture(t, true)
			var before protocol.App
			f.update(func() { f.failNext, before = test.failReload, f.served[0] })
			for i, files := range test.saves {
				f.save(files, test.output[i])
			}
			requests := f.reloadRequests()
			if len(requests) != test.reloads {
				t.Fatalf("reloads = %d", len(requests))
			}
			if test.reloads != 0 && !test.failReload {
				request := requests[0]
				if request.Session != "gentle-otter-482" || request.App != "alpha" {
					t.Fatalf("reload_app %+v", request)
				}
				if before.Overlay == "" || before.Overlay == request.Digest {
					t.Fatalf("create_session overlay %q, reloaded %s", before.Overlay, request.Digest)
				}
				if !strings.Contains(string(f.overlay(t, before.Overlay)["server.py"]), "app = None") {
					t.Fatal("the overlay a session starts from lacks the server source")
				}
				entries := f.overlay(t, request.Digest)
				if string(entries["tools.py"]) != "second" || !strings.Contains(string(entries[".holon/main.py"]), `start("server")`) {
					t.Fatalf("overlay entries = %v", entries)
				}
			}
			f.mu.Lock()
			after := f.served[0]
			f.mu.Unlock()
			if rebuilt := after.Image != before.Image; rebuilt != test.rebuilt {
				t.Fatalf("image replaced = %v", rebuilt)
			}
			if test.rebuilt {
				for name, content := range test.saves[len(test.saves)-1] {
					if saved := string(f.overlay(t, after.Overlay)[strings.TrimPrefix(name, "alpha/")]); saved != content {
						t.Fatalf("the rebuilt app runs %s %q", name, saved)
					}
				}
			}
		})
	}
}

func TestDevPicksUpTheSessionAnEarlierRunLeft(t *testing.T) {
	for _, test := range []struct {
		name    string
		between func(t *testing.T, f *reloadFixture)
		edit    map[string]string
		creates int
		reloads int
		rebuilt bool
		apps    []string // besides alpha
	}{
		{name: "server source edited since reloads into the kept image", edit: map[string]string{"alpha/tools.py": "second"}, creates: 1, reloads: 1},
		{
			name: "an app added to the project since joins the session", edit: map[string]string{manifestFileName: alphaManifest + betaRemote},
			creates: 1, apps: []string{"beta"},
		},
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
			if f.created() != test.creates {
				t.Fatalf("creates = %d, want %d; stderr=%q", f.created(), test.creates, f.stderr.String())
			}
			if rebuilt := after.Image != before.Image; len(f.reloadRequests()) != test.reloads || rebuilt != test.rebuilt {
				t.Fatalf("reloads = %d, image replaced = %v", len(f.reloadRequests()), rebuilt)
			}
			if names := f.apps(); !slices.Equal(names, append([]string{"alpha"}, test.apps...)) {
				t.Fatalf("session apps = %v", names)
			}
		})
	}
}

// A project change applies to the running session: an app joins it or leaves it, and the others keep their images.
// A change that fails to apply is applied with the next save.
func TestDevAppliesProjectChanges(t *testing.T) {
	f := startReloadFixture(t, true)
	f.mu.Lock()
	image := f.served[0].Image
	f.mu.Unlock()
	const deploying = "Preparing apps\nalpha uploaded\nDeploying apps\nalpha deploying\n"
	for _, step := range []struct {
		files          map[string]string
		fail           bool
		output, served string
	}{
		{
			files: map[string]string{manifestFileName: alphaManifest + betaRemote}, fail: true, served: "alpha",
			output: deploying + "beta deploying\nalpha deployment failed\nbeta deployment failed\nproject replacement outcome conflicts with current desired state\n" + watching + "\n",
		},
		{
			files: map[string]string{"alpha/tools.py": "second"}, served: "alpha beta",
			output: deploying + "beta deploying\nalpha reloading\nalpha reloaded in 1.2 s\nproject ok\nalpha ready\nbeta ready\n" + watching + "\n",
		},
		{
			files: map[string]string{manifestFileName: alphaManifest}, served: "alpha",
			output: deploying + "beta removed\nalpha ready\n" + watching + "\n",
		},
	} {
		f.update(func() { f.failSave = step.fail })
		f.save(step.files, step.output)
		if apps := strings.Join(f.apps(), " "); apps != step.served {
			t.Fatalf("session apps = %v, want %v", apps, step.served)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.creates != 1 || f.served[0].Image != image || !strings.Contains(f.stdout.String(), "/mcp/beta") {
		t.Fatalf("creates = %d, image %s, was %s; stdout=%q", f.creates, f.served[0].Image, image, f.stdout.String())
	}
}

func TestDevDetachedStartsItsOwnSession(t *testing.T) {
	f := startReloadFixture(t, true)
	if err := f.stop(); err != nil {
		t.Fatalf("first run: %v stderr=%q", err, f.stderr.String())
	}
	f.start("--detach")
	f.await(func() bool { return f.created() == 2 })
	f.update(func() {})
	f.await(func() bool { return strings.Contains(f.stdout.String(), "Expires") })
}

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
	f.await(func() bool { return strings.Contains(f.stdout.String(), "Starting") })
	f.update(func() { f.ready = true })
	f.watching()
	// Ready repeats only its heading when the endpoints are the ones Starting listed.
	if strings.Count(f.stdout.String(), "/mcp/alpha") != 1 || !strings.HasSuffix(f.stdout.String(), "\nReady\n") {
		t.Fatalf("stdout = %q", f.stdout.String())
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
	if err := os.RemoveAll(filepath.Join(alpha, "node_modules", "holon-sh")); err != nil {
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
