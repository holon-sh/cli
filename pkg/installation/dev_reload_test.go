package installation

import (
	"archive/tar"
	"bytes"
	"context"
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
	failNext bool
	failSave bool
	foreign  bool
	creates  int
	served   []protocol.App
	revision int
	changed  chan struct{} // closed when the session moves past revision
	reads    []string      // the revision each get_session named
	reloads  []reloadAppRequest

	holdBuilds    bool
	held, stopped int
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
		f.served[slices.IndexFunc(f.served, func(app protocol.App) bool { return app.Name == input.App })].Overlay = input.Digest
		f.moved()
		installationtest.WriteJSON(w, reloadAppResult{ElapsedSeconds: 1.2})
	})
	built := buildImage(t, "built")
	h.start(t, func(ctx context.Context, request buildRequest, progress io.Writer) (string, error) {
		f.mu.Lock()
		hold := f.holdBuilds
		if hold {
			f.held++
		}
		f.mu.Unlock()
		if !hold {
			return built(ctx, request, progress)
		}
		<-ctx.Done()
		f.mu.Lock()
		f.stopped++
		f.mu.Unlock()
		return "", ctx.Err()
	})
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

func (f *reloadFixture) save(files map[string]string, line string) {
	f.t.Helper()
	from := len(f.stderr.String())
	f.mu.Lock()
	reads := len(f.reads)
	f.mu.Unlock()
	sourceFiles(f.t, f.root, files)
	f.await(func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return len(f.reads) > reads && strings.Contains(f.stderr.String()[from:], line)
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

func TestDevSaves(t *testing.T) {
	for _, test := range []struct {
		name                 string
		failReload, failSave bool
		saves                []map[string]string
		lines                []string
		reloads              int
		rebuilt              bool
	}{
		{
			name:  "server source restarts the app in place, and the same source again does nothing",
			saves: []map[string]string{{"alpha/tools.py": "second"}, {"alpha/tools.py": "second"}},
			lines: []string{"alpha reloaded in 1.2 s", "alpha unchanged"}, reloads: 1,
		},
		{
			name:  "files an editor writes while saving do nothing",
			saves: []map[string]string{{"alpha/.tools.py.swp": "swap", "alpha/tools.py~": "first", manifestFileName: alphaManifest + "# comment\n"}},
			lines: []string{""},
		},
		{
			name: "a rebuild the session failed to take is built again with the next save", failSave: true,
			saves: []map[string]string{{"alpha/pyproject.toml": alphaProject + "dependencies = [\"idna\"]\n"}, {"alpha/tools.py": "second"}},
			lines: []string{"remote update failed", "alpha ready"}, rebuilt: true,
		},
		{
			name: "a failed reload is reported and rebuilds nothing", failReload: true,
			saves: []map[string]string{{"alpha/tools.py": "second"}},
			lines: []string{"alpha reload failed"}, reloads: 1,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := startReloadFixture(t, true)
			var before protocol.App
			f.update(func() { f.failNext, f.failSave, before = test.failReload, test.failSave, f.served[0] })
			for i, files := range test.saves {
				f.save(files, test.lines[i])
			}
			f.mu.Lock()
			after := f.served[0]
			f.mu.Unlock()
			if reloads, rebuilt := len(f.reloadRequests()), after.Image != before.Image; reloads != test.reloads || rebuilt != test.rebuilt {
				t.Fatalf("reloads = %d, image replaced = %v", reloads, rebuilt)
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
		apps    []string
	}{
		{name: "server source edited since reloads into the kept image", edit: map[string]string{"alpha/tools.py": "second"}, creates: 1, reloads: 1},
		{
			name: "a reload that fails is not reported ready", edit: map[string]string{"alpha/tools.py": "second"}, creates: 1, reloads: 1,
			between: func(_ *testing.T, f *reloadFixture) { f.failNext = true },
		},
		{
			name: "an app added to the project since joins the session", edit: map[string]string{manifestFileName: alphaManifest + betaRemote},
			creates: 1, apps: []string{"beta"},
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
			if len(f.reloadRequests()) != test.reloads || after.Image != before.Image {
				t.Fatalf("reloads = %d, image %s, was %s", len(f.reloadRequests()), after.Image, before.Image)
			}
			if names := f.apps(); !slices.Equal(names, append([]string{"alpha"}, test.apps...)) {
				t.Fatalf("session apps = %v", names)
			}
			if strings.Contains(f.stderr.String(), "alpha reload failed") == strings.Contains(f.stderr.String(), "alpha ready") {
				t.Fatalf("stderr=%q", f.stderr.String())
			}
		})
	}
}

func TestDevAppliesProjectChanges(t *testing.T) {
	f := startReloadFixture(t, true)
	f.mu.Lock()
	image := f.served[0].Image
	f.mu.Unlock()
	for _, step := range []struct {
		files        map[string]string
		fail         bool
		line, served string
	}{
		{files: map[string]string{manifestFileName: alphaManifest + betaRemote}, fail: true, line: "project replacement outcome conflicts", served: "alpha"},
		{files: map[string]string{"alpha/tools.py": "second"}, line: "beta ready", served: "alpha beta"},
		{files: map[string]string{manifestFileName: alphaManifest}, line: "beta removed", served: "alpha"},
	} {
		f.update(func() { f.failSave = step.fail })
		f.save(step.files, step.line)
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

func TestDevReloadPushesTheBuildSnapshot(t *testing.T) {
	f := startReloadFixture(t, true)
	f.mu.Lock()
	first := f.served[0].Overlay
	f.mu.Unlock()
	f.save(map[string]string{"alpha/tools.py": "second"}, "alpha reloaded")
	request := f.reloadRequests()[0]
	if request.Session != "gentle-otter-482" || request.App != "alpha" {
		t.Fatalf("reload_app %+v", request)
	}
	if !strings.Contains(string(f.overlay(t, first)["server.py"]), "app = None") {
		t.Fatal("the overlay a session starts from lacks the server source")
	}
	entries := f.overlay(t, request.Digest)
	// The snapshot an image build would read, with its launcher.
	if string(entries["tools.py"]) != "second" || !strings.Contains(string(entries[".holon/main.py"]), `start("server")`) {
		t.Fatalf("overlay entries = %v", entries)
	}
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
	if strings.Count(f.stdout.String(), "/mcp/alpha") != 1 || !strings.Contains(f.stdout.String(), "\nReady\n") {
		t.Fatalf("stdout = %q", f.stdout.String())
	}
	// Each read names the revision the session last had, so the server holds it until the session moves.
	f.await(func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return slices.Equal(f.reads, []string{"0", "1"})
	})
}

func TestDevSaveStopsTheLastPreparation(t *testing.T) {
	f := startReloadFixture(t, true)
	counts := func() (held, stopped int) {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.held, f.stopped
	}
	f.mu.Lock()
	f.holdBuilds = true
	f.mu.Unlock()
	sourceFiles(t, f.root, map[string]string{"alpha/uv.lock": "# edited\n" + alphaLock})
	f.await(func() bool {
		held, _ := counts()
		return held == 1
	})
	sourceFiles(t, f.root, map[string]string{"alpha/tools.py": "second"})
	f.await(func() bool {
		held, stopped := counts()
		return held == 2 && stopped == 1
	})
}

func TestDevInstallsNothingOnThisMachine(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	f := startReloadFixture(t, true)
	from := len(f.stderr.String())
	sourceFiles(t, f.root, map[string]string{"alpha/package.json": `{"private":true,"dependencies":{"holon-sh":"0.0.1"}}`})
	f.await(func() bool { return strings.Contains(f.stderr.String()[from:], "alpha ready") })
	data, err := os.ReadFile(filepath.Join(f.root, "alpha", "package.json"))
	if err != nil || !strings.Contains(string(data), `"#holon-sh"`) {
		t.Fatalf("package.json = %s, %v", data, err)
	}
}
