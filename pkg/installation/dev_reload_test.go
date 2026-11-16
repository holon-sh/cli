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
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	testingclock "k8s.io/utils/clock/testing"

	"github.com/holon-sh/cli/internal/testfiles"
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
	harness  *runHarness
	clock    *testingclock.FakeClock
	stdout   *installationtest.ObservedOutput
	stderr   *installationtest.ObservedOutput
	await    func(check func() bool)
	start    func(args ...string)
	stop     func() error

	mu        sync.Mutex
	ready     bool
	ambiguous bool
	outage    sets.Set[string]
	updates   int
	failNext  bool
	refusal   string
	failSave  bool
	unsaved   bool
	creates   int
	served    []protocol.App
	revision  int
	changed   chan struct{}
	reads     []string
	reloads   []sentSource
	running   map[string][]byte
}

const sessionUID = "5b0c2f1e-7d4a-4c8e-9a61-2f3b8e0d4c71"

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
	f.registry = newImageRegistry()
	registry := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		unsaved := f.unsaved
		f.mu.Unlock()
		if unsaved && strings.HasSuffix(r.URL.Path, "/manifests/"+sessionUID) {
			_, _ = io.Copy(io.Discard, r.Body)
			<-r.Context().Done()
			return
		}
		f.registry.ServeHTTP(w, r)
	}))
	t.Cleanup(registry.Close)
	h := newRunHarness(t)
	f.harness = h
	h.control.Handle([]string{protocol.ToolGetAppContract}, nil, func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		running, ok := f.running[installationtest.ToolArguments(r)["app"].(string)]
		f.mu.Unlock()
		if !ok {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		tools := string(overlayEntries(t, running)["tools.py"])
		contract := strings.NewReplacer(`"lookup"`, `"lookup_`+tools+`"`, `"description":"alpha"`, `"description":"`+tools+`"`).Replace(servedContract)
		installationtest.WriteJSON(w, json.RawMessage(contract))
	})
	h.control.Handle([]string{protocol.ToolUploadGrant}, nil, func(w http.ResponseWriter, r *http.Request) {
		installationtest.AssertOrigin(t, r, "dev."+h.baseHost)
		grant := buildGrant(registry)
		grant.Repositories["beta"], grant.Repositories["gamma"] = "holon/upload/beta", "holon/upload/gamma"
		installationtest.WriteJSON(w, grant)
	})
	session := func() map[string]any {
		response := apiSession("alpha")
		response["uid"] = sessionUID
		response["apps"], response["revision"] = f.served, strconv.Itoa(f.revision)
		if !f.ready {
			response["conditions"] = []protocol.Condition{{Type: protocol.ConditionReady, Status: "False", Reason: "Provisioning"}}
		}
		return response
	}
	h.control.Handle([]string{protocol.ToolCreateSession, protocol.ToolGetSession, protocol.ToolRenewSession, protocol.ToolUpdateSession},
		nil, func(w http.ResponseWriter, r *http.Request) {
			installationtest.AssertOrigin(t, r, "dev."+h.baseHost)
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
			switch tool := installationtest.ToolName(r); {
			case f.outage != nil:
				f.outage.Insert(tool)
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			case tool == protocol.ToolCreateSession:
				f.creates++
				f.served, f.running = input.Apps, map[string][]byte{}
				if f.ambiguous {
					panic(http.ErrAbortHandler)
				}
			case tool == protocol.ToolUpdateSession:
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
				f.updates++
				f.moved()
			case tool == protocol.ToolGetSession:
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
	h.mux.HandleFunc("GET /sessions/gentle-otter-482/logs", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "2026-11-09T10:00:00.1Z  alpha starting up\n2026-11-09T10:00:00.2Z INFO alpha tools/call lookup ok 22ms\n")
		_ = http.NewResponseController(w).Flush()
		<-r.Context().Done()
	})
	h.mux.HandleFunc("POST /sessions/{id}/apps/{app}/source", func(w http.ResponseWriter, r *http.Request) {
		installationtest.AssertOrigin(t, r, "dev."+h.baseHost)
		if kind := r.Header.Get(protocol.HeaderContentType); kind != runtimecontract.OverlayMediaType {
			t.Errorf("source request: %q", kind)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return
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
		f.running[r.PathValue("app")] = body
		installationtest.WriteJSON(w, protocol.ReloadAppResult{Output: installed})
	})
	h.start(t)
	fc := testingclock.NewFakeClock(time.Now())
	h.run.Clock, f.clock = fc, fc
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
	f.harness.files.save(f.t, f.root, files)
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
		unsaved    bool
		saves      []map[string]string
		lines      []string
		reloads    int
	}{
		{
			name:  "files an editor writes while saving do nothing",
			saves: []map[string]string{{"alpha/.tools.py.swp": "swap", "alpha/tools.py~": "first", manifestFileName: alphaManifest + "# comment\n"}},
			lines: []string{""},
		},
		{
			name: "a failed reload is reported, and the source before it is sent again", failReload: true,
			saves: []map[string]string{{"alpha/tools.py": "second"}, {"alpha/tools.py": "first"}},
			lines: []string{"alpha failed", "alpha ready"}, reloads: 2,
		},
		{
			name: "a source not saved within a minute is reported, and one a newer save replaced is not", unsaved: true,
			saves: []map[string]string{{"alpha/tools.py": "second"}, {"alpha/tools.py": "third"}},
			lines: []string{"alpha ready in", "alpha serves this save, but a restart loses it: no answer within a minute"}, reloads: 2,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := startReloadFixture(t, true)
			started := len(f.reloadRequests())
			f.update(func() { f.failNext, f.unsaved = test.failReload, test.unsaved })
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

func TestDevKeepsARefusedSaveReported(t *testing.T) {
	f := startReloadFixture(t, true)
	f.update(func() { f.unsaved = true })
	f.save(map[string]string{"alpha/tools.py": "second"}, "alpha ready in")
	f.update(func() { f.failNext = true })
	f.save(map[string]string{"alpha/tools.py": "third"}, "alpha failed")
	f.clock.Step(time.Minute)
	if err := f.stop(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(f.stderr.String(), "a restart loses it") {
		t.Fatalf("the upload of an earlier save replaced the failure: %q", f.stderr.String())
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
		{
			name: "a reload that fails is neither reported ready nor uploaded", creates: 1, reloads: 1,
			between: func(_ *testing.T, f *reloadFixture) { f.failNext = true },
		},
		{
			name: "an app that no longer runs is started", creates: 1, reloads: 1, uploads: 1,
			between: func(_ *testing.T, f *reloadFixture) { f.running = map[string][]byte{} },
		},
		{
			name: "an app added to the project since joins the session", edit: map[string]string{manifestFileName: alphaManifest + betaRemote},
			creates: 1, reloads: 1, uploads: 1, apps: []string{"beta"},
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
			failed := strings.Contains(f.stderr.String(), "alpha failed")
			if failed == strings.Contains(f.stderr.String(), "alpha ready") || failed == strings.Contains(f.stdout.String(), "\nReady\n") {
				t.Fatalf("stdout=%q stderr=%q", f.stdout.String(), f.stderr.String())
			}
		})
	}
}

func TestDevResumesTheAppsAnEarlierRunStarted(t *testing.T) {
	f := startReloadFixture(t, true, map[string]string{"alpha/server.py": "import mcp\n", "alpha/package.json": `{"private":true,"dependencies":{"holon-ui":"0.0.1"}}`})
	resume := func(tools string) (before, after []sentSource) {
		t.Helper()
		if err := f.stop(); err != nil {
			t.Fatal(err)
		}
		before = f.reloadRequests()
		sourceFiles(t, f.root, map[string]string{"alpha/tools.py": tools})
		f.start()
		f.watching()
		return before, f.reloadRequests()[len(before):]
	}
	if before, after := resume("first"); len(after) != 1 || after[0].Digest != before[len(before)-1].Digest {
		t.Fatalf("an unchanged source was sent %d times, not as it runs", len(after))
	}
	runs := func(tools string) {
		t.Helper()
		resume(tools)
		f.mu.Lock()
		running := overlayEntries(t, f.running["alpha"])
		f.mu.Unlock()
		if client := string(running["generated/holon-ui/index.mjs"]); string(running["tools.py"]) != tools || !strings.Contains(client, "lookup_"+tools) {
			t.Fatalf("after an edit to %q alpha runs %q with the clients %q", tools, running["tools.py"], client)
		}
	}
	runs("second")
	const undescribed = ""
	resume(undescribed)
	runs("third")
}

func TestDevAppliesProjectChanges(t *testing.T) {
	f := startReloadFixture(t, true)
	f.update(func() { f.served = append(f.served, protocol.App{Name: "linear", Inherited: true}) })
	for _, step := range []struct {
		files        map[string]string
		fail         bool
		line, served string
	}{
		{files: map[string]string{manifestFileName: alphaManifest + betaRemote}, fail: true, line: "manifest replacement outcome conflicts", served: "alpha linear"},
		{files: map[string]string{"alpha/tools.py": "second"}, line: "beta ready", served: "alpha beta linear"},
		{files: map[string]string{manifestFileName: alphaManifest}, line: "beta removed", served: "alpha linear"},
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

func TestDevSessionLifecycle(t *testing.T) {
	f := startReloadFixture(t, true, map[string]string{
		manifestFileName: alphaManifest + "- name: beta\n  access: {server: {}}\n  path: ./beta\n",
		"beta/server.py": "app = None", "beta/tools.py": "first",
		"beta/pyproject.toml": strings.ReplaceAll(alphaProject, "alpha", "beta"), "beta/uv.lock": strings.ReplaceAll(alphaLock, "alpha", "beta"),
	})
	config := f.harness.run.Auth.ConfigDir
	sourceFiles(t, config, map[string]string{"config.json": `{"origin":"https://other.example"}`})
	f.save(map[string]string{"alpha/tools.py": "second"}, "alpha ready in")
	f.update(func() { f.outage = sets.New[string]() })
	stepUntil(t, f.clock, renewalInterval, func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.outage.HasAll(protocol.ToolGetSession, protocol.ToolRenewSession)
	})
	f.update(func() { f.outage = nil })
	if err := f.stop(); err != nil {
		t.Fatal(err)
	}
	var reloads []string
	for _, sent := range f.reloadRequests() {
		reloads = append(reloads, sent.App)
	}
	f.mu.Lock()
	if f.updates != 0 || !slices.Equal(reloads, []string{"alpha", "beta", "alpha"}) || strings.Count(f.stdout.String(), "Ready\n") != 1 {
		t.Fatalf("updates = %d, reloads = %v, stdout=%q", f.updates, reloads, f.stdout.String())
	}
	f.mu.Unlock()
	sourceFiles(t, config, map[string]string{"config.json": `{"origin":"` + f.harness.server.URL + `"}`})
	f.update(func() { f.ambiguous = true })
	f.start("--detach")
	f.await(func() bool { return f.created() == 2 })
	if err := f.stop(); err == nil || !strings.Contains(err.Error(), "outcome is unknown; inspect sessions at https://dev."+f.harness.baseHost+" before retrying") ||
		f.created() != 2 {
		t.Fatalf("ambiguous creation: creates=%d error=%v", f.created(), err)
	}
}

func TestDevSaveToAnAppRemovedFromTheSession(t *testing.T) {
	f := startReloadFixture(t, true)
	started := len(f.reloadRequests())
	f.update(func() { f.served = nil })
	f.save(map[string]string{"alpha/tools.py": "second"}, "alpha failed: application removed from the session")
	if sent := len(f.reloadRequests()) - started; sent != 0 {
		t.Fatalf("sources sent = %d", sent)
	}
}

func TestDevSendsSourceAndNoImage(t *testing.T) {
	f := startReloadFixture(t, true)
	f.mu.Lock()
	app := f.served[0]
	f.mu.Unlock()
	if app.Image != "" || app.Port != 0 || !app.Source {
		t.Fatalf("session app = %+v", app)
	}
	if overlay := overlayEntries(t, f.reloadRequests()[0].body); !strings.Contains(string(overlay["server.py"]), "app = None") {
		t.Fatalf("first overlay entries = %v", overlay)
	}
	f.save(map[string]string{"alpha/tools.py": "second"}, "alpha ready")
	request := f.reloadRequests()[1]
	if request.Session != "gentle-otter-482" || request.App != "alpha" {
		t.Fatalf("source sent to %s %s", request.Session, request.App)
	}
	if err := f.stop(); err != nil {
		t.Fatal(err)
	}
	var tagged ocispec.Manifest
	err := json.Unmarshal(f.item("manifests/"+sessionUID), &tagged)
	if err != nil || tagged.ArtifactType != runtimecontract.OverlayMediaType || len(tagged.Layers) != 1 ||
		tagged.Layers[0].Digest.String() != request.Digest || !bytes.Equal(f.item("blobs/"+request.Digest), request.body) ||
		string(overlayEntries(t, request.body)["tools.py"]) != "second" {
		t.Fatalf("the session's tag names %+v, want %s: %v", tagged.Layers, request.Digest, err)
	}
}

func TestOverlayPlan(t *testing.T) {
	build := `{"scripts":{"build":"vite build"}}`
	python := map[string]string{"pyproject.toml": "", "uv.lock": "", "package.json": build, "pnpm-lock.yaml": ""}
	serve := []string{".venv/bin/python", "-m", "holon.serve", "src/server.py"}
	for _, test := range []struct {
		name, entry string
		files       map[string]string
		page        bool
		want        runtimecontract.Plan
	}{
		{"a Node app whose page waits for its clients", "src/server.ts", map[string]string{"package.json": build, "package-lock.json": ""}, false, runtimecontract.Plan{
			Install: runtimecontract.Install{Commands: [][]string{{"npm", "install"}}}, Start: []string{"node_modules/.bin/holon-serve", "src/server.ts"},
		}},
		{"a Python app with a page", "src/server.py", python, true, runtimecontract.Plan{
			Install: runtimecontract.Install{Commands: [][]string{{"uv", "sync"}, {"pnpm", "install"}}}, Prepare: [][]string{{"pnpm", "run", "build"}}, Start: serve,
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			testfiles.Write(t, root, test.files)
			plan, err := overlayPlan(root, test.entry, test.page)
			plan.Install.Inputs = ""
			if err != nil || !reflect.DeepEqual(plan, test.want) {
				t.Fatalf("plan = %+v, want %+v: %v", plan, test.want, err)
			}
		})
	}
	t.Run("the install's inputs change with what it installs from", func(t *testing.T) {
		root := t.TempDir()
		files := map[string]string{"server.py": "", ".python-version": "3.13", "pyproject.toml": "", "uv.lock": "", "package.json": build, "package-lock.json": ""}
		testfiles.Write(t, root, files)
		install := func(page bool) runtimecontract.Install {
			plan, err := overlayPlan(root, "server.py", page)
			if err != nil {
				t.Fatal(err)
			}
			return plan.Install
		}
		last := install(false)
		if !reflect.DeepEqual(install(true), last) {
			t.Fatal("building the page changed the install")
		}
		for _, name := range slices.Sorted(maps.Keys(files)) {
			testfiles.Write(t, root, map[string]string{name: files[name] + "\n"})
			if next := install(true); (next.Inputs != last.Inputs) != (name != "server.py") {
				t.Fatalf("changing %s changed the install's inputs: %t", name, next.Inputs != last.Inputs)
			} else {
				last = next
			}
		}
	})
}

func (f *reloadFixture) uploads() int {
	f.registry.mu.Lock()
	defer f.registry.mu.Unlock()
	return len(f.registry.uploads)
}

func (f *reloadFixture) item(name string) []byte {
	f.registry.mu.Lock()
	defer f.registry.mu.Unlock()
	return f.registry.items["holon/upload/alpha/"+name].data
}

func overlayEntries(t *testing.T, overlay []byte) map[string][]byte {
	t.Helper()
	decoder, err := zstd.NewReader(bytes.NewReader(overlay))
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
			name: "a dependency chain starts each server with the clients of the apps it requires", app: "gamma", client: "generated/holon-sh/index.mjs",
			files: map[string]string{
				manifestFileName:     gammaManifest + "- name: beta\n  access: {server: {}}\n  path: ./beta\n",
				"beta/server.json":   strings.ReplaceAll(requiresAlpha, "/gamma", "/beta"),
				"beta/server.ts":     "import { apps } from \"#holon-sh\";\nimport { McpServer } from \"@modelcontextprotocol/server\";\n",
				"beta/package.json":  `{"private":true,"dependencies":{"holon-sh":"0.0.1"}}`,
				"gamma/server.json":  strings.ReplaceAll(requiresAlpha, `"alpha"`, `"beta"`),
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
				"gamma/uv.lock":        "version = 1\n",
			},
			sends: []send{{clients: true}},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := startReloadFixture(t, true, map[string]string{"alpha/server.py": "import mcp\n"}, test.files)
			var sends []send
			for _, reload := range f.reloadRequests() {
				if reload.App == test.app {
					overlay := overlayEntries(t, reload.body)
					var plan runtimecontract.Plan
					_ = json.Unmarshal(overlay[runtimecontract.PlanFile], &plan)
					sends = append(sends, send{clients: strings.Contains(string(overlay[test.client]), "lookup"), page: plan.Prepare != nil})
				}
			}
			_, err := os.Stat(filepath.Join(f.root, test.app, test.client))
			if !slices.Equal(sends, test.sends) || (err == nil) != test.sends[len(test.sends)-1].clients ||
				strings.Count(f.stderr.String(), test.app+" ready") != 1 {
				t.Fatalf("%s sends = %+v, local %s: %v; stderr=%q", test.app, sends, test.client, err, f.stderr.String())
			}
		})
	}
}

func TestDevShowsReloadDetailsOnlyWhenVerbose(t *testing.T) {
	f := startReloadFixture(t, true)
	f.save(map[string]string{"alpha/tools.py": "second"}, "alpha ready")
	f.update(func() { f.refusal = installFailure })
	f.save(map[string]string{"alpha/tools.py": "third"}, "alpha failed: npm install: exit status 1")
	if err := f.stop(); err != nil {
		t.Fatal(err)
	}
	for _, hidden := range []string{installed, "npm error 404", "Timing ", "grant-secret", "alpha starting up", "tools/call"} {
		if output := f.stdout.String() + f.stderr.String(); strings.Contains(output, hidden) {
			t.Fatalf("quiet dev showed %q in %q", hidden, output)
		}
	}
	f.start("--verbose")
	f.await(func() bool {
		output := f.stderr.String()
		return strings.Contains(output, installed) && strings.Contains(output, "Timing alpha reload:")
	})
	f.update(func() { f.refusal = installFailure })
	f.save(map[string]string{"alpha/tools.py": "fourth"}, "npm error 404\nnot found\nalpha failed: npm install: exit status 1\n")
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
}

func TestDevInstallsNothingOnThisMachine(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	f := startReloadFixture(t, true)
	f.harness.files.save(t, f.root, map[string]string{"alpha/package.json": `{"private":true,"dependencies":{"holon-sh":"0.0.1"}}`})
	f.await(func() bool {
		data, _ := os.ReadFile(filepath.Join(f.root, "alpha", "package.json"))
		return strings.Contains(string(data), `"#holon-sh"`)
	})
}
