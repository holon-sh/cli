package installation

import (
	"archive/tar"
	"bytes"
	"context"
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

func TestImageBuildInput(t *testing.T) {
	for _, relative := range []string{
		"holon.yaml", "server.json", "railpack.json", "Dockerfile", ".dockerignore", "pyproject.toml", "uv.lock",
		"package.json", "package-lock.json", "pnpm-lock.yaml", "yarn.lock", "requirements.txt", "go.mod",
		"ui/index.tsx", "ui", "vite.config.ts", "tsconfig.json", "nested/package.json",
	} {
		if !imageBuildInput(relative) {
			t.Errorf("imageBuildInput(%q) = false", relative)
		}
	}
	for _, relative := range []string{"tools.py", "server.py", "lib/store.ts", "docs/ui/page.md", "uilib.py"} {
		if imageBuildInput(relative) {
			t.Errorf("imageBuildInput(%q) = true", relative)
		}
	}
}

func TestReloadAppRequestNamesExactlyTheProtocolArguments(t *testing.T) {
	data, err := json.Marshal(reloadAppRequest{
		AppRequest: protocol.AppRequest{SessionRequest: protocol.SessionRequest{Session: "s"}, App: "a"}, Digest: "d", MediaType: overlayMediaType,
	})
	want := `{"session":"s","app":"a","digest":"d","mediaType":"application/vnd.holon.source-overlay.v0.tar+zstd"}`
	if err != nil || string(data) != want {
		t.Fatalf("request = %s (%v), want %s", data, err, want)
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

func TestWriteOverlayIsStableAndLeavesOutBuildMetadata(t *testing.T) {
	root := t.TempDir()
	sourceFiles(t, root, map[string]string{
		"server.py": "server", "lib/tools.py": "tools", ".holon/main.py": "launcher", ".holon-types/railpack.json": "{}",
	})
	if err := os.Symlink("lib/tools.py", filepath.Join(root, "alias.py")); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "overlay")
	first, err := writeOverlay(root, archive)
	if err != nil {
		t.Fatal(err)
	}
	if first.MediaType != overlayMediaType {
		t.Fatalf("media type = %s", first.MediaType)
	}
	info, err := os.Stat(archive)
	if err != nil || info.Size() != first.Size {
		t.Fatalf("size = %d, file %v %v", first.Size, info, err)
	}
	got := overlayEntries(t, archive)
	want := map[string]string{
		"server.py": "server", "lib/": "", "lib/tools.py": "tools", ".holon/": "", ".holon/main.py": "launcher", "alias.py": "-> lib/tools.py",
	}
	if len(got) != len(want) {
		t.Fatalf("entries = %v, want %v", got, want)
	}
	for name, contents := range want {
		if got[name] != contents {
			t.Fatalf("%s = %q, want %q", name, got[name], contents)
		}
	}

	if err := os.Chtimes(filepath.Join(root, "server.py"), time.Unix(1000, 0), time.Unix(1000, 0)); err != nil {
		t.Fatal(err)
	}
	second, err := writeOverlay(root, archive)
	if err != nil || second.Digest != first.Digest {
		t.Fatalf("touched snapshot digest = %s, want %s (%v)", second.Digest, first.Digest, err)
	}
	sourceFiles(t, root, map[string]string{"server.py": "edited"})
	third, err := writeOverlay(root, archive)
	if err != nil || third.Digest == first.Digest {
		t.Fatalf("edited snapshot digest = %s (%v)", third.Digest, err)
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
	reads    int
	unready  bool
	readyAt  time.Time
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

func startReloadFixture(t *testing.T) *reloadFixture {
	t.Helper()
	f := &reloadFixture{readyAt: time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)}
	var h *runHarness
	f.root, h, f.registry = alphaProject(t, map[string]string{
		"alpha/tools.py": "first", "alpha/pyproject.toml": "[project]\nname = \"alpha\"\n",
	})
	var served []protocol.App
	h.control.Handle([]string{protocol.ToolCreateSession, protocol.ToolGetSession, protocol.ToolRenewSession, protocol.ToolUpdateSession, protocol.ToolDeleteSession},
		nil, func(w http.ResponseWriter, r *http.Request) {
			f.mu.Lock()
			defer f.mu.Unlock()
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
			case protocol.ToolUpdateSession:
				var input protocol.UpdateSessionRequestFor[protocol.AppsRequestFor[protocol.App]]
				if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
					t.Error(err)
				}
				served = input.Desired.Apps
				f.updates++
			}
			response := apiSession(h, "alpha")
			response["apps"], response["revision"] = served, strconv.Itoa(f.revision)
			status := map[bool]string{false: "True", true: "False"}[f.unready]
			response["conditions"] = []protocol.Condition{{Type: protocol.ConditionReady, Status: status, LastTransitionTime: protocol.Time(f.readyAt)}}
			f.reads++
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
	f := startReloadFixture(t)
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
	if entries["tools.py"] != "second" || !strings.Contains(entries[".holon/main.mjs"], "../server.js") {
		t.Fatalf("overlay entries = %v", entries)
	}

	// A save that leaves the snapshot's bytes as they were pushes and restarts nothing.
	started = len(f.stderr.String())
	sourceFiles(t, f.root, map[string]string{"alpha/tools.py": "second"})
	f.await(func() bool { return strings.Contains(f.stderr.String()[started:], "alpha unchanged") })
	if requests := f.reloadRequests(); len(requests) != 1 || f.blobs(repository) != 1 {
		t.Fatalf("identical snapshot: reloads=%d blobs=%d", len(requests), f.blobs(repository))
	}

	// A renewal moves only the revision and restarts nothing.
	observed := func(change func()) {
		t.Helper()
		f.mu.Lock()
		change()
		reads := f.reads
		f.mu.Unlock()
		f.await(func() bool {
			f.mu.Lock()
			defer f.mu.Unlock()
			return f.reads > reads+1
		})
	}
	observed(func() { f.revision++ })
	if requests := f.reloadRequests(); len(requests) != 1 {
		t.Fatalf("a renewal restarted the app: %+v", requests)
	}
	// A restarted pod comes back on its image, so its overlay is sent again without a save, retried
	// while the runtime is still starting.
	started = len(f.stderr.String())
	f.failNextReload()
	observed(func() { f.unready = true })
	observed(func() { f.unready, f.readyAt = false, f.readyAt.Add(2*time.Second) })
	f.await(func() bool {
		return strings.Contains(f.stderr.String()[started:], "alpha restarted, reloaded in 1.2 s")
	})
	if requests := f.reloadRequests(); len(requests) != 3 || requests[1].Digest != requests[0].Digest || requests[2].Digest != requests[0].Digest {
		t.Fatalf("restart re-sent %+v", requests)
	}

	// A file the image build consumes takes the rebuild path.
	started = len(f.stderr.String())
	sourceFiles(t, f.root, map[string]string{"alpha/pyproject.toml": "[project]\nname = \"renamed\"\n"})
	f.await(func() bool { return strings.Contains(f.stderr.String()[started:], "alpha building") })
	f.await(func() bool {
		return strings.Contains(f.stderr.String()[started:], "continuing to watch") || f.updateCount() != 0
	})
	if requests := f.reloadRequests(); len(requests) != 3 {
		t.Fatalf("pyproject.toml edit reloaded: %+v", requests)
	}
	if err := f.stop(); err != nil {
		t.Fatalf("run: %v stderr=%q", err, f.stderr.String())
	}
}

func TestDevFallsBackToARebuildWhenAReloadFails(t *testing.T) {
	f := startReloadFixture(t)
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
	f := startReloadFixture(t)
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

func TestWaitSessionReadyLongPollsFromTheLastRevision(t *testing.T) {
	h := newRunHarness(t)
	var mu sync.Mutex
	var seen []any
	h.control.Handle([]string{protocol.ToolGetSession}, nil, func(w http.ResponseWriter, r *http.Request) {
		var input map[string]any
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		mu.Lock()
		defer mu.Unlock()
		if input["waitSeconds"] != float64(sessionWaitSeconds) {
			t.Errorf("waitSeconds = %v", input["waitSeconds"])
		}
		seen = append(seen, input["revision"])
		response := apiSession(h, "alpha")
		response["revision"] = strconv.Itoa(len(seen))
		if len(seen) < 3 {
			response["conditions"] = []protocol.Condition{{Type: protocol.ConditionReady, Status: "False", Reason: "Provisioning"}}
		}
		installationtest.WriteJSON(w, response)
	})
	h.start(t, nil)
	manager, err := NewClient(t.Context(), h.run.Auth)
	if err != nil {
		t.Fatal(err)
	}
	var accepted protocol.SessionResponse
	data, err := json.Marshal(apiSession(h, "alpha"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &accepted); err != nil {
		t.Fatal(err)
	}
	accepted.Revision = "0"
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	if _, err := waitSessionReady(ctx, manager, "gentle-otter-482", accepted, nil); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if want := []any{"0", "1", "2"}; !slices.Equal(seen, want) {
		t.Fatalf("revisions = %v, want %v", seen, want)
	}
}

func TestDevStopsWhenTheInstallationRefusesALongPoll(t *testing.T) {
	root, h, _ := alphaProject(t, nil)
	h.control.Handle([]string{protocol.ToolCreateSession, protocol.ToolGetSession, protocol.ToolRenewSession, protocol.ToolDeleteSession},
		nil, func(w http.ResponseWriter, r *http.Request) {
			switch installationtest.ToolName(r) {
			case protocol.ToolDeleteSession:
				installationtest.WriteJSON(w, protocol.DeleteSessionResultFor[protocol.SessionResponse]{Deleted: true})
			case protocol.ToolGetSession:
				w.WriteHeader(http.StatusBadRequest)
			default:
				response := apiSession(h, "alpha")
				response["conditions"] = []protocol.Condition{{Type: protocol.ConditionReady, Status: "False", Reason: "Provisioning"}}
				installationtest.WriteJSON(w, response)
			}
		})
	h.start(t, buildImage(t, "built"))
	err := executeCommand(t.Context(), []string{"dev", "--project", filepath.Join(root, manifestFileName)}, io.Discard, io.Discard, h.run)
	if !errors.Is(err, errSessionWaitRefused) {
		t.Fatalf("dev = %v, want %v", err, errSessionWaitRefused)
	}
}

func TestASaveInterruptsALongPoll(t *testing.T) {
	root, h, _ := alphaProject(t, nil)
	waiting := make(chan struct{}, 16)
	h.control.Handle([]string{protocol.ToolCreateSession, protocol.ToolGetSession, protocol.ToolRenewSession, protocol.ToolDeleteSession},
		nil, func(w http.ResponseWriter, r *http.Request) {
			if installationtest.ToolName(r) == protocol.ToolDeleteSession {
				installationtest.WriteJSON(w, protocol.DeleteSessionResultFor[protocol.SessionResponse]{Deleted: true})
				return
			}
			var input map[string]any
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Error(err)
			}
			if input["waitSeconds"] != nil {
				waiting <- struct{}{}
				<-r.Context().Done()
			}
			response := apiSession(h, "alpha")
			response["conditions"] = []protocol.Condition{{Type: protocol.ConditionReady, Status: "False", Reason: "Provisioning"}}
			installationtest.WriteJSON(w, response)
		})
	build := buildImage(t, "built")
	var builds sync.Mutex
	count := 0
	h.start(t, func(ctx context.Context, request buildRequest, progress io.Writer) (string, error) {
		builds.Lock()
		count++
		builds.Unlock()
		return build(ctx, request, progress)
	})
	fc := testingclock.NewFakeClock(time.Now())
	h.run.Clock = fc
	_, stderr, stop := startDev(t, h.run, "--project", filepath.Join(root, manifestFileName))
	stepUntil(t, fc, devDebounce, func() bool { return len(waiting) != 0 })
	sourceFiles(t, root, map[string]string{"alpha/server.js": "export default {edited: true}"})
	stepUntil(t, fc, devDebounce, func() bool {
		builds.Lock()
		defer builds.Unlock()
		return count == 2
	})
	if err := stop(); err != nil {
		t.Fatalf("run: %v stderr=%q", err, stderr.String())
	}
}
