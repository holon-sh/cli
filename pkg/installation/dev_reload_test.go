package installation

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
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

func TestChangeClassification(t *testing.T) {
	for relative, want := range map[string]bool{
		"tools.py": true, "lib/store.ts": true, "server.mjs": true,
		"ui/main.ts": false, "pyproject.toml": false, "data/menu.json": false, "ui": false,
	} {
		if got := serverSource(relative); got != want {
			t.Errorf("serverSource(%q) = %v, want %v", relative, got, want)
		}
	}
	// The watcher ignores what editors write while saving, but not dotfiles that builds read.
	for name, want := range map[string]bool{
		".tools.py.swp": true, "tools.py~": true, "4913": true, ".#tools.py": true, "tools.py___jb_tmp___": true,
		"tools.py": false, ".python-version": false, ".dockerignore": false,
	} {
		if got := editorFile(name); got != want {
			t.Errorf("editorFile(%q) = %v, want %v", name, got, want)
		}
	}
}

func overlayEntries(t *testing.T, path string) map[string]string {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	decoder, err := zstd.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer decoder.Close()
	entries := map[string]string{}
	archive := tar.NewReader(decoder)
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			return entries
		}
		if err != nil {
			t.Fatal(err)
		}
		if !header.ModTime.Equal(time.Unix(0, 0)) || header.Uid != 0 || header.Gid != 0 || header.Uname != "" {
			t.Errorf("%s keeps host metadata: %+v", header.Name, header)
		}
		data, err := io.ReadAll(archive)
		if err != nil {
			t.Fatal(err)
		}
		entries[header.Name] = string(data)
		if header.Typeflag == tar.TypeSymlink {
			entries[header.Name] = "-> " + header.Linkname
		}
	}
}

// reloadFixture runs holon dev for one source app against a fake control plane and registry.
type reloadFixture struct {
	root     string
	registry *imageRegistry
	stdout   *installationtest.ObservedOutput
	stderr   *installationtest.ObservedOutput
	await    func(check func() bool)
	stop     func() error

	mu       sync.Mutex
	reloads  []reloadAppRequest
	updates  int
	failNext bool
	revision int
	changed  chan struct{} // closed when the session moves past revision
	reads    []string      // the revision each get_session named
}

// update changes the session and moves it past its revision, which releases any held read.
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

func (f *reloadFixture) readCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.reads)
}

func (f *reloadFixture) reloadRequests() []reloadAppRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.reloads)
}

func (f *reloadFixture) updateCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.updates
}

func (f *reloadFixture) failNextReload() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failNext = true
}

func (f *reloadFixture) blobs(repository string) int {
	f.registry.mu.Lock()
	defer f.registry.mu.Unlock()
	count := 0
	for name, item := range f.registry.items {
		if strings.HasPrefix(name, repository+"/blobs/") && bytes.HasPrefix(item.data, []byte{0x28, 0xb5, 0x2f, 0xfd}) {
			count++
		}
	}
	return count
}

// alphaProject writes a project with one source app, alpha, plus extra files, and serves its upload grant
// from a fresh registry. Tests answer the session tools themselves.
func alphaProject(t *testing.T, extra map[string]string) (string, *runHarness, *imageRegistry) {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		manifestFileName:  "apps:\n- name: alpha\n  access: {server: {}}\n  path: ./alpha\n",
		"alpha/server.js": "export default {}", "alpha/package.json": sourcePackage,
	}
	maps.Copy(files, extra)
	sourceFiles(t, root, files)
	registry, server := buildRegistry(t)
	h := newRunHarness(t)
	h.control.Handle([]string{protocol.ToolUploadGrant}, nil, func(w http.ResponseWriter, _ *http.Request) {
		installationtest.WriteJSON(w, buildGrant(server))
	})
	return root, h, registry
}

func startReloadFixture(t *testing.T, extra map[string]string) *reloadFixture {
	t.Helper()
	f := &reloadFixture{changed: make(chan struct{})}
	var h *runHarness
	files := map[string]string{"alpha/tools.py": "first", "alpha/pyproject.toml": "[project]\nname = \"alpha\"\n"}
	maps.Copy(files, extra)
	f.root, h, f.registry = alphaProject(t, files)
	if err := os.Symlink("tools.py", filepath.Join(f.root, "alpha", "alias.py")); err != nil {
		t.Fatal(err)
	}
	var served []protocol.App
	h.control.Handle([]string{protocol.ToolCreateSession, protocol.ToolGetSession, protocol.ToolRenewSession, protocol.ToolUpdateSession, protocol.ToolDeleteSession},
		nil, func(w http.ResponseWriter, r *http.Request) {
			f.mu.Lock()
			defer f.mu.Unlock()
			switch installationtest.ToolName(r) {
			case protocol.ToolGetSession:
				var input struct {
					Revision string `json:"revision"`
				}
				if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
					t.Error(err)
				}
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
			case protocol.ToolDeleteSession:
				installationtest.WriteJSON(w, protocol.DeleteSessionResultFor[protocol.SessionResponse]{Deleted: true})
				return
			case protocol.ToolCreateSession:
				var input protocol.AppsRequestFor[protocol.App]
				if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
					t.Error(err)
				}
				served = input.Apps
			case protocol.ToolUpdateSession:
				var input protocol.UpdateSessionRequestFor[protocol.AppsRequestFor[protocol.App]]
				if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
					t.Error(err)
				}
				served = input.Desired.Apps
				f.updates++
				f.moved()
			}
			response := apiSession(h, "alpha")
			response["apps"], response["revision"] = served, strconv.Itoa(f.revision)
			installationtest.WriteJSON(w, response)
		})
	h.control.Handle([]string{toolReloadApp}, nil, func(w http.ResponseWriter, r *http.Request) {
		installationtest.AssertOrigin(t, r, "dev."+h.baseHost)
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
		restarted := time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)
		installationtest.WriteJSON(w, reloadAppResult{RestartedAt: restarted, ReadyAt: restarted.Add(time.Second), Digest: input.Digest, ElapsedSeconds: 1.2})
	})
	h.start(t, buildImage(t, "built"))
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
	f.await(func() bool { return strings.Contains(f.stdout.String(), "Watching for changes.") })
	return f
}

func TestDevReloadsSourceEditsInPlace(t *testing.T) {
	// Only image builds read .holon-types, so a source overlay leaves it out.
	f := startReloadFixture(t, map[string]string{"alpha/.holon-types/notes.txt": "build only"})
	repository := "holon/upload/alpha"

	started := len(f.stderr.String())
	sourceFiles(t, f.root, map[string]string{"alpha/tools.py": "second"})
	f.await(func() bool { return strings.Contains(f.stderr.String()[started:], "alpha reloaded in 1.2 s") })
	requests := f.reloadRequests()
	if len(requests) != 1 || requests[0].Session != "gentle-otter-482" || requests[0].App != "alpha" || requests[0].MediaType != overlayMediaType ||
		!protocol.ValidDigest(requests[0].Digest) {
		t.Fatalf("reload requests = %+v", requests)
	}
	if f.updateCount() != 0 {
		t.Fatalf("a source edit rolled out %d images", f.updateCount())
	}
	f.registry.mu.Lock()
	blob, pushed := f.registry.items[repository+"/blobs/"+requests[0].Digest]
	f.registry.mu.Unlock()
	if !pushed {
		t.Fatal("the overlay was not pushed before reload_app")
	}
	archive := filepath.Join(t.TempDir(), "overlay")
	if err := os.WriteFile(archive, blob.data, 0o600); err != nil {
		t.Fatal(err)
	}
	entries := overlayEntries(t, archive)
	if entries["tools.py"] != "second" || !strings.Contains(entries[".holon/main.mjs"], "../server.js") ||
		entries["alias.py"] != "-> tools.py" || slices.ContainsFunc(slices.Collect(maps.Keys(entries)), func(name string) bool {
		return strings.HasPrefix(name, ".holon-types")
	}) {
		t.Fatalf("overlay entries = %v", entries)
	}

	// A save that leaves the snapshot's bytes as they were, which touches only their times, pushes and
	// restarts nothing.
	started = len(f.stderr.String())
	sourceFiles(t, f.root, map[string]string{"alpha/tools.py": "second"})
	f.await(func() bool { return strings.Contains(f.stderr.String()[started:], "alpha unchanged") })
	if requests := f.reloadRequests(); len(requests) != 1 || f.blobs(repository) != 1 {
		t.Fatalf("identical snapshot: reloads=%d blobs=%d", len(requests), f.blobs(repository))
	}

	// The fixture holds every read that names the current revision, so each reload above interrupted one.
	// A change releases it, and the next read names the revision it brought.
	reads := f.readCount()
	f.update(func() {})
	f.await(func() bool { return f.readCount() > reads })
	f.mu.Lock()
	if last := f.reads[len(f.reads)-1]; last != strconv.Itoa(f.revision) {
		t.Errorf("read after the session moved named revision %q, want %d", last, f.revision)
	}
	f.mu.Unlock()

	// A file the image build consumes takes the rebuild path.
	started = len(f.stderr.String())
	sourceFiles(t, f.root, map[string]string{"alpha/pyproject.toml": "[project]\nname = \"renamed\"\n"})
	f.await(func() bool { return strings.Contains(f.stderr.String()[started:], "alpha building") })
	f.await(func() bool {
		return strings.Contains(f.stderr.String()[started:], "continuing to watch") || f.updateCount() != 0
	})
	if requests := f.reloadRequests(); len(requests) != 1 {
		t.Fatalf("pyproject.toml edit reloaded: %+v", requests)
	}
	if err := f.stop(); err != nil {
		t.Fatalf("run: %v stderr=%q", err, f.stderr.String())
	}
}

func TestDevFallsBackToARebuildWhenAReloadFails(t *testing.T) {
	f := startReloadFixture(t, nil)
	started := len(f.stderr.String())
	f.failNextReload()
	sourceFiles(t, f.root, map[string]string{"alpha/tools.py": "second"})
	f.await(func() bool {
		output := f.stderr.String()[started:]
		return strings.Contains(output, "alpha reload failed") && strings.Contains(output, "rebuilding") && strings.Contains(output, "alpha building")
	})
	if requests := f.reloadRequests(); len(requests) != 1 {
		t.Fatalf("reload requests = %+v", requests)
	}
	f.await(func() bool {
		return strings.Contains(f.stderr.String()[started:], "continuing to watch") || f.updateCount() != 0
	})
}

func TestDevReportsExtractionFailureAfterTheReload(t *testing.T) {
	f := startReloadFixture(t, nil)
	started := len(f.stderr.String())
	// Mentioning an MCP SDK makes this app an MCP server, whose extraction cannot reach the test's builder.
	sourceFiles(t, f.root, map[string]string{"alpha/server.js": `import "@modelcontextprotocol/sdk"; export default {}`})
	f.await(func() bool { return strings.Contains(f.stderr.String()[started:], "\nMCP types ") })
	output := f.stderr.String()[started:]
	reloaded, failed := strings.Index(output, "alpha reloaded in"), strings.Index(output, "\nMCP types ")
	if reloaded < 0 || failed < reloaded {
		t.Fatalf("reload line must precede the extraction failure: %q", output)
	}
	if f.updateCount() != 0 {
		t.Fatalf("extraction failure rolled out %d images", f.updateCount())
	}
}

func TestRunDevPrintsURLsBeforeTheSessionIsReady(t *testing.T) {
	root, h, _ := alphaProject(t, nil)
	var mu sync.Mutex
	ready := false
	h.control.Handle([]string{protocol.ToolCreateSession, protocol.ToolGetSession, protocol.ToolRenewSession, protocol.ToolDeleteSession},
		nil, func(w http.ResponseWriter, r *http.Request) {
			if installationtest.ToolName(r) == protocol.ToolDeleteSession {
				installationtest.WriteJSON(w, protocol.DeleteSessionResultFor[protocol.SessionResponse]{Deleted: true})
				return
			}
			mu.Lock()
			defer mu.Unlock()
			response := apiSession(h, "alpha")
			if !ready {
				// As the API does, publish no endpoints until the apps are up.
				response["endpoints"] = []protocol.SessionEndpoint{}
				response["conditions"] = []protocol.Condition{{Type: protocol.ConditionReady, Status: "False", Reason: "Provisioning"}}
			}
			installationtest.WriteJSON(w, response)
		})
	h.start(t, buildImage(t, "built"))
	fc := testingclock.NewFakeClock(time.Now())
	h.run.Clock = fc
	stdout, stderr, stop := startDev(t, h.run, "--project", filepath.Join(root, manifestFileName))

	endpoints := "  MCP tools (all apps)  https://gentle-otter-482." + h.baseHost + "/mcp\n\n" +
		"  alpha\n    Browser  https://gentle-otter-482." + h.baseHost + "/alpha/\n" +
		"    MCP      https://gentle-otter-482." + h.baseHost + "/mcp/alpha\n\n"
	starting := "Session  gentle-otter-482\n\nStarting\n" + endpoints
	stepUntil(t, fc, devDebounce, func() bool { return strings.Contains(stdout.String(), "alpha\n") })
	if got := stdout.String(); got != starting {
		t.Fatalf("stdout before ready = %q, want %q", got, starting)
	}
	mu.Lock()
	ready = true
	mu.Unlock()
	stepUntil(t, fc, devDebounce, func() bool { return strings.Contains(stdout.String(), "Watching for changes.") })
	want := starting + "\nReady\n" + endpoints + "Watching for changes. Ctrl-C stops this session.\n"
	if got := stdout.String(); got != want {
		t.Fatalf("stdout after ready = %q, want %q", got, want)
	}
	if err := stop(); err != nil {
		t.Fatalf("run: %v stderr=%q", err, stderr.String())
	}
}
