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
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/opencontainers/go-digest"
	testingclock "k8s.io/utils/clock/testing"

	"github.com/holon-sh/cli/pkg/installation/installationtest"
	"github.com/holon-sh/protocol"
	"github.com/holon-sh/protocol/runtimecontract"
)

const (
	alphaProject  = "[project]\nname = \"alpha\"\nversion = \"1\"\n"
	alphaManifest = "apps:\n- name: alpha\n  access: {server: {}}\n  path: ./alpha\n"
	betaRemote    = "- name: beta\n  access: {server: {}}\n  remote: {url: https://upstream.example/mcp}\n"
	alphaLock     = "version = 1\n\n[[package]]\nname = \"alpha\"\nversion = \"1\"\nsource = { virtual = \".\" }\n"
)

const installed = "added 1 package\n"
const installFailure = `{"error":{"code":"invalid_request","message":"npm install: exit status 1"},"output":"npm error 404\nnot found\n"}`

type reloadFixture struct {
	t        *testing.T
	root     string
	registry *imageRegistry
	files    *fakeFiles
	stdout   *installationtest.ObservedOutput
	stderr   *installationtest.ObservedOutput
	await    func(check func() bool)
	start    func(args ...string)
	stop     func() error

	mu         sync.Mutex
	ready      bool
	failNext   bool
	refusal    string
	failSave   bool
	unserved   bool
	unrecorded bool
	creates    int
	started    string
	uploaded   int
	served     []protocol.App
	revision   int
	changed    chan struct{}
	reads      []string
	reloads    []sentSource
	recorded   []string
}

type sentSource struct {
	Session, App, Digest string
	body                 []byte
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

func (f *reloadFixture) reloadRequests() []sentSource {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.reloads)
}

func startReloadFixture(t *testing.T, ready bool, files ...map[string]string) *reloadFixture {
	t.Helper()
	f := &reloadFixture{t: t, root: t.TempDir(), ready: ready, changed: make(chan struct{})}
	sourceFiles(t, f.root, map[string]string{
		manifestFileName:  alphaManifest,
		"alpha/server.py": "app = None", "alpha/tools.py": "first", "alpha/pyproject.toml": alphaProject, "alpha/uv.lock": alphaLock,
	})
	for _, more := range files {
		sourceFiles(t, f.root, more)
	}
	var registry *httptest.Server
	f.registry, registry = buildRegistry(t)
	h := newRunHarness(t)
	h.control.Handle([]string{toolGetAppContract}, nil, func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		unserved := f.unserved
		f.mu.Unlock()
		if unserved {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		installationtest.WriteJSON(w, json.RawMessage(servedContract))
	})
	f.files = &h.files
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
				f.served, f.started, f.uploaded = input.Apps, input.Apps[0].Overlay, f.uploads()
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
	h.mux.HandleFunc("POST /sessions/{id}/apps/{app}/source", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil || r.Header.Get(protocol.HeaderContentType) != runtimecontract.OverlayMediaType {
			t.Errorf("source request: %v %q", err, r.Header.Get(protocol.HeaderContentType))
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		f.reloads = append(f.reloads, sentSource{r.PathValue("id"), r.PathValue("app"), digest.FromBytes(body).String(), body})
		if f.failNext {
			f.failNext = false
			w.WriteHeader(http.StatusConflict)
			return
		}
		if f.refusal != "" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, f.refusal)
			f.refusal = ""
			return
		}
		f.unserved = false
		installationtest.WriteJSON(w, reloadAppResult{ElapsedSeconds: 1.2, Output: installed})
	})
	h.control.Handle([]string{toolRecordAppSource}, nil, func(w http.ResponseWriter, r *http.Request) {
		var input recordAppSourceRequest
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		f.mu.Lock()
		unrecorded := f.unrecorded
		f.mu.Unlock()
		if unrecorded {
			<-r.Context().Done()
			installationtest.WriteJSON(w, struct{}{})
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		f.served[slices.IndexFunc(f.served, func(app protocol.App) bool { return app.Name == input.App })].Overlay = input.Digest
		f.recorded = append(f.recorded, input.Digest)
		f.moved()
		installationtest.WriteJSON(w, struct{}{})
	})
	h.start(t, nil)
	fc := testingclock.NewFakeClock(time.Now())
	h.run.Clock = fc
	f.await = func(check func() bool) {
		t.Helper()
		stepUntil(t, fc, devDebounce, check)
	}
	f.start = func(args ...string) {
		t.Helper()
		var stop func() error
		f.stdout, f.stderr, stop = startDev(t, h.run, append([]string{"--file", filepath.Join(f.root, manifestFileName)}, args...)...)
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
	var reads int
	f.await(func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		reads = len(f.reads)
		return reads > 0 && f.reads[reads-1] == strconv.Itoa(f.revision)
	})
	from := len(f.stderr.String())
	f.files.save(f.t, f.root, files)
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
		name       string
		failReload bool
		unrecorded bool
		saves      []map[string]string
		lines      []string
		reloads    int
	}{
		{
			name:  "server source restarts the app in place, and the same source again does nothing",
			saves: []map[string]string{{"alpha/tools.py": "second"}, {"alpha/tools.py": "second"}},
			lines: []string{"alpha reloaded in 1.2 s", ""}, reloads: 1,
		},
		{
			name:  "files an editor writes while saving do nothing",
			saves: []map[string]string{{"alpha/.tools.py.swp": "swap", "alpha/tools.py~": "first", manifestFileName: alphaManifest + "# comment\n"}},
			lines: []string{""},
		},
		{
			name: "a failed reload is reported", failReload: true,
			saves: []map[string]string{{"alpha/tools.py": "second"}},
			lines: []string{"alpha reload failed"}, reloads: 1,
		},
		{
			name: "a source the session has not recorded within a minute is reported, and one a newer save replaced is not", unrecorded: true,
			saves: []map[string]string{{"alpha/tools.py": "second"}, {"alpha/tools.py": "third"}},
			lines: []string{"alpha reloaded in 1.2 s", "alpha reloaded, but not recorded in the session: no answer within a minute"}, reloads: 2,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := startReloadFixture(t, true)
			started := len(f.reloadRequests())
			f.update(func() { f.failNext, f.unrecorded = test.failReload, test.unrecorded })
			for i, files := range test.saves {
				f.save(files, test.lines[i])
			}
			if reloads := len(f.reloadRequests()) - started; reloads != test.reloads {
				t.Fatalf("reloads = %d, want %d", reloads, test.reloads)
			}
			if strings.Contains(f.stderr.String(), errSourceSuperseded.Error()) {
				t.Fatalf("a replaced source was reported: %q", f.stderr.String())
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
		uploads int
		apps    []string
	}{
		{name: "unchanged source is neither sent nor uploaded", creates: 1},
		{
			name: "server source edited since reloads into the session", edit: map[string]string{"alpha/tools.py": "second"},
			creates: 1, reloads: 1, uploads: 1,
		},
		{
			name: "a reload that fails is neither reported ready nor uploaded", edit: map[string]string{"alpha/tools.py": "second"}, creates: 1, reloads: 1,
			between: func(_ *testing.T, f *reloadFixture) { f.failNext = true },
		},
		{
			name: "an app that never started is sent its source", creates: 1, reloads: 1, uploads: 1,
			between: func(_ *testing.T, f *reloadFixture) { f.unserved = true },
		},
		{
			name: "an app added to the project since joins the session", edit: map[string]string{manifestFileName: alphaManifest + betaRemote},
			creates: 1, apps: []string{"beta"},
		},
		{
			name: "the same apps from another directory start their own session", creates: 2, reloads: 1, uploads: 1,
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
			f.update(func() {
				if test.between != nil {
					test.between(t, f)
				}
			})
			sourceFiles(t, f.root, test.edit)
			uploaded, reloaded := f.uploads(), len(f.reloadRequests())
			f.start()
			f.watching()
			if err := f.stop(); err != nil {
				t.Fatalf("second run: %v stderr=%q", err, f.stderr.String())
			}
			if uploads := f.uploads() - uploaded; uploads != test.uploads {
				t.Fatalf("uploads = %d, want %d", uploads, test.uploads)
			}
			if f.created() != test.creates {
				t.Fatalf("creates = %d, want %d; stderr=%q", f.created(), test.creates, f.stderr.String())
			}
			if reloads := len(f.reloadRequests()) - reloaded; reloads != test.reloads {
				t.Fatalf("reloads = %d, want %d", reloads, test.reloads)
			}
			if names := f.apps(); !slices.Equal(names, append([]string{"alpha"}, test.apps...)) {
				t.Fatalf("session apps = %v", names)
			}
			f.mu.Lock()
			recorded := f.served[slices.IndexFunc(f.served, func(app protocol.App) bool { return app.Name == "alpha" })].Overlay
			f.mu.Unlock()
			if f.blob(recorded) == nil {
				t.Fatalf("the session names source %s, which the registry does not hold", recorded)
			}
			failed := strings.Contains(f.stderr.String(), "alpha reload failed")
			if failed == strings.Contains(f.stderr.String(), "alpha ready") || failed == strings.Contains(f.stdout.String(), "\nReady\n") {
				t.Fatalf("stdout=%q stderr=%q", f.stdout.String(), f.stderr.String())
			}
		})
	}
}

func TestDevAppliesProjectChanges(t *testing.T) {
	f := startReloadFixture(t, true)
	for _, step := range []struct {
		files        map[string]string
		fail         bool
		line, served string
	}{
		{files: map[string]string{manifestFileName: alphaManifest + betaRemote}, fail: true, line: "manifest replacement outcome conflicts", served: "alpha"},
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
	if f.creates != 1 || !strings.Contains(f.stdout.String(), "/mcp/beta") {
		t.Fatalf("creates = %d; stdout=%q", f.creates, f.stdout.String())
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

func TestDevSaveToAnAppRemovedFromTheSession(t *testing.T) {
	f := startReloadFixture(t, true)
	started := len(f.reloadRequests())
	f.update(func() { f.served = nil })
	f.save(map[string]string{"alpha/tools.py": "second"}, "alpha reload failed")
	if sent := len(f.reloadRequests()) - started; sent != 0 {
		t.Fatalf("sources sent = %d", sent)
	}
}

func TestDevSendsSourceAndNoImage(t *testing.T) {
	f := startReloadFixture(t, true)
	f.mu.Lock()
	app := f.served[0]
	f.mu.Unlock()
	if app.Image != "" || app.Port != 0 {
		t.Fatalf("session app = %+v", app)
	}
	if first, overlay := f.reloadRequests()[0], f.overlay(t, f.started); first.Digest != f.started ||
		!strings.Contains(string(overlay["server.py"]), "app = None") || string(overlay[runtimecontract.PrepareFile]) != "uv sync\n" ||
		string(overlay[runtimecontract.StartFile]) != "exec uv run --no-sync python -m holon.serve server.py\n" {
		t.Fatalf("the session was created from %s and first sent %s", f.started, first.Digest)
	}
	if f.uploaded != 0 || strings.Contains(f.stderr.String(), "alpha uploaded") {
		t.Fatalf("uploads before the session existed = %d; stderr=%q", f.uploaded, f.stderr.String())
	}
	if started := f.stderr.String(); strings.Index(started, "alpha ready") < strings.Index(started, "alpha reloaded") {
		t.Fatalf("the app was reported ready before its source had started: %q", started)
	}
	from := len(f.stderr.String())
	f.save(map[string]string{"alpha/tools.py": "second"}, "alpha reloaded")
	if !strings.Contains(f.stderr.String()[from:], "alpha reloaded") {
		t.Fatalf("stderr=%q", f.stderr.String())
	}
	request := f.reloadRequests()[1]
	if request.Session != "gentle-otter-482" || request.App != "alpha" {
		t.Fatalf("source sent to %s %s", request.Session, request.App)
	}
	f.await(func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return slices.Contains(f.recorded, request.Digest)
	})
	entries := f.overlay(t, request.Digest)
	if !bytes.Equal(request.body, f.blob(request.Digest)) || string(entries["tools.py"]) != "second" {
		t.Fatalf("overlay entries = %v", entries)
	}
}

func TestOverlayScripts(t *testing.T) {
	const python = "exec uv run --no-sync python -m holon.serve "
	for _, test := range []struct {
		name, entry    string
		files          map[string]string
		prepare, start string
	}{
		{
			name: "Node", entry: "src/server.ts", files: map[string]string{"package.json": `{}`, "package-lock.json": ""},
			prepare: "{ cat package.json package-lock.json | cmp -s - .holon/installed || { rm -f .holon/installed && npm install && cat package.json package-lock.json >.holon/installed; }; }\n",
			start:   "exec node_modules/.bin/holon-serve src/server.ts\n",
		},
		{
			name: "Python with a frontend", entry: "src/server.py",
			files:   map[string]string{"package.json": `{"scripts":{"build":"vite build"}}`, "pnpm-lock.yaml": ""},
			prepare: "uv sync && { cat package.json pnpm-lock.yaml | cmp -s - .holon/installed || { rm -f .holon/installed && pnpm install && cat package.json pnpm-lock.yaml >.holon/installed; }; } && pnpm run build\n",
			start:   python + "src/server.py\n",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			sourceFiles(t, root, test.files)
			if prepare, start, err := overlayScripts(root, test.entry, true); err != nil || prepare != test.prepare || start != test.start {
				t.Fatalf("scripts = %q, %q: %v", prepare, start, err)
			}
		})
	}
}

func TestPrepareSkipsOnlyAnUnchangedInstall(t *testing.T) {
	root, bin := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "npm"), []byte("#!/bin/sh\necho >>installs\n! grep -q fail package-lock.json\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	for i, step := range []struct {
		manifest, lock string
		installs       int
	}{{`{}`, "1", 1}, {`{}`, "1", 1}, {`{"x":1}`, "1", 2}, {`{"x":1}`, "fail", 3}, {`{"x":1}`, "1", 4}} {
		sourceFiles(t, root, map[string]string{"package.json": step.manifest, "package-lock.json": step.lock})
		prepare, _, err := overlayScripts(root, "server.ts", false)
		if err != nil {
			t.Fatal(err)
		}
		sourceFiles(t, root, map[string]string{runtimecontract.PrepareFile: prepare})
		command := exec.Command("sh", runtimecontract.PrepareFile)
		command.Dir = root
		err = command.Run()
		log, _ := os.ReadFile(filepath.Join(root, "installs"))
		if installs := bytes.Count(log, []byte("\n")); (err != nil) != (step.lock == "fail") || installs != step.installs {
			t.Fatalf("step %d: %d installs, want %d: %v", i, installs, step.installs, err)
		}
	}
}

func (f *reloadFixture) uploads() int {
	f.registry.mu.Lock()
	defer f.registry.mu.Unlock()
	return len(f.registry.uploads)
}

func (f *reloadFixture) blob(digest string) []byte {
	f.registry.mu.Lock()
	defer f.registry.mu.Unlock()
	return f.registry.items["holon/upload/alpha/blobs/"+digest].data
}

func (f *reloadFixture) overlay(t *testing.T, digest string) map[string][]byte {
	t.Helper()
	blob := f.blob(digest)
	for _, sent := range f.reloadRequests() {
		if sent.Digest == digest {
			blob = sent.body
		}
	}
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

func TestDevSendsClientsAndPagesOnFirstStart(t *testing.T) {
	const (
		ui            = "generated/holon-ui/index.mjs"
		requiresAlpha = `{"name":"example.com/gamma","version":"1","description":"gamma","_meta":{"io.modelcontextprotocol.registry/publisher-provided":{"sh.holon":{"requires":{"alpha":{}}}}}}`
		gammaManifest = alphaManifest + "- name: gamma\n  access: {server: {}}\n  path: ./gamma\n"
	)
	type send struct{ clients, page bool }
	for _, test := range []struct {
		name        string
		files       map[string]string
		app, client string
		sends       []send
	}{
		{name: "an app with nothing to generate or build is sent once", app: "alpha", client: ui, sends: []send{{}}},
		{
			name: "clients generated from the running app follow in a second send", app: "alpha", client: ui,
			files: map[string]string{"alpha/package.json": `{"private":true,"dependencies":{"holon-ui":"0.0.1"}}`},
			sends: []send{{}, {clients: true}},
		},
		{
			name: "a page is built once its clients are in the source", app: "alpha", client: ui,
			files: map[string]string{"alpha/package.json": `{"private":true,"scripts":{"build":"vite build"},"dependencies":{"holon-ui":"0.0.1"}}`},
			sends: []send{{}, {clients: true, page: true}},
		},
		{
			name: "a page that takes no client is sent again to be built", app: "alpha", client: ui,
			files: map[string]string{"alpha/package.json": `{"private":true,"scripts":{"build":"vite build"}}`},
			sends: []send{{}, {page: true}},
		},
		{
			name: "a TypeScript server is first sent with the clients of the apps it requires", app: "gamma", client: "generated/holon-sh/index.mjs",
			files: map[string]string{
				manifestFileName: gammaManifest, "gamma/server.json": requiresAlpha,
				"gamma/server.ts":    "import { apps } from \"#holon-sh\";\nimport { McpServer } from \"@modelcontextprotocol/server\";\n",
				"gamma/package.json": `{"private":true,"scripts":{"build":"vite build"},"dependencies":{"holon-sh":"0.0.1","holon-ui":"0.0.1"}}`,
			},
			sends: []send{{clients: true}, {clients: true, page: true}},
		},
		{
			name: "a Python server is first sent with the clients of the apps it requires", app: "gamma", client: "holon/apps/__init__.py",
			files: map[string]string{
				manifestFileName: gammaManifest, "gamma/server.json": requiresAlpha,
				"gamma/server.py":      "from holon.apps import apps\nimport mcp\n",
				"gamma/pyproject.toml": "[project]\nname = \"gamma\"\nversion = \"1\"\ndependencies = [\"holon-sh\"]\n",
			},
			sends: []send{{clients: true}},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := startReloadFixture(t, true, map[string]string{"alpha/server.py": "import mcp\n"}, test.files)
			var sends []send
			for _, reload := range f.reloadRequests() {
				if reload.App == test.app {
					overlay := f.overlay(t, reload.Digest)
					sends = append(sends, send{
						clients: strings.Contains(string(overlay[test.client]), "lookup"),
						page:    strings.Contains(string(overlay[runtimecontract.PrepareFile]), " run build"),
					})
				}
			}
			_, err := os.Stat(filepath.Join(f.root, test.app, test.client))
			if !slices.Equal(sends, test.sends) || (err == nil) != test.sends[len(test.sends)-1].clients {
				t.Fatalf("%s sends = %+v, local %s: %v; stderr=%q", test.app, sends, test.client, err, f.stderr.String())
			}
		})
	}
}

func TestDevShowsReloadDetailsOnlyWhenVerbose(t *testing.T) {
	f := startReloadFixture(t, true)
	f.save(map[string]string{"alpha/tools.py": "second"}, "alpha reloaded")
	f.update(func() { f.refusal = installFailure })
	f.save(map[string]string{"alpha/tools.py": "third"}, "alpha reload failed: npm install: exit status 1")
	if err := f.stop(); err != nil {
		t.Fatal(err)
	}
	if output := f.stderr.String(); strings.Contains(output, installed) || strings.Contains(output, "npm error 404") || strings.Contains(output, "Timing ") {
		t.Fatalf("quiet stderr = %q", output)
	}
	f.start("--verbose")
	f.await(func() bool {
		output := f.stderr.String()
		return strings.Contains(output, installed+"alpha reloaded") && strings.Contains(output, "Timing alpha reload:")
	})
	f.update(func() { f.refusal = installFailure })
	f.save(map[string]string{"alpha/tools.py": "fourth"}, "npm error 404\nnot found\nalpha reload failed: npm install: exit status 1\n")
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
		return slices.Equal(f.reads, []string{"0", "1", "2"})
	})
}

func TestDevInstallsNothingOnThisMachine(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	f := startReloadFixture(t, true)
	f.files.save(t, f.root, map[string]string{"alpha/package.json": `{"private":true,"dependencies":{"holon-sh":"0.0.1"}}`})
	f.await(func() bool {
		data, _ := os.ReadFile(filepath.Join(f.root, "alpha", "package.json"))
		return strings.Contains(string(data), `"#holon-sh"`)
	})
}
