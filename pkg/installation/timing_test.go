package installation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moby/buildkit/client"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	testingclock "k8s.io/utils/clock/testing"
	"oras.land/oras-go/v2/content"

	"github.com/holon-sh/cli/pkg/installation/installationtest"
	"github.com/holon-sh/protocol"
)

const timingManifest = "apps:\n- name: alpha\n  access: {server: {}}\n  path: ./alpha\n"

// timingSession serves one app's session lifecycle for dev and deploy, echoing the apps it is given.
type timingSession struct {
	mu         sync.Mutex
	apps       any
	generation int64
	notReady   int
	transition time.Time
	onCreate   func()
}

func newTimingHarness(t *testing.T, session *timingSession, build buildApplication) (*runHarness, string) {
	t.Helper()
	root := t.TempDir()
	sourceFiles(t, root, map[string]string{
		manifestFileName: timingManifest, "alpha/server.js": "export default {}", "alpha/package.json": sourcePackage,
	})
	_, registry := buildRegistry(t)
	h := newRunHarness(t)
	h.control.Handle([]string{protocol.ToolUploadGrant}, nil, func(w http.ResponseWriter, _ *http.Request) {
		installationtest.WriteJSON(w, buildGrant(registry))
	})
	h.control.Handle([]string{
		protocol.ToolCreateSession, protocol.ToolGetSession, protocol.ToolUpdateSession, protocol.ToolRenewSession, protocol.ToolDeleteSession,
	}, nil, func(w http.ResponseWriter, r *http.Request) {
		session.mu.Lock()
		defer session.mu.Unlock()
		tool, arguments := installationtest.ToolName(r), installationtest.ToolArguments(r)
		switch tool {
		case protocol.ToolDeleteSession:
			installationtest.WriteJSON(w, protocol.DeleteSessionResultFor[protocol.SessionResponse]{Deleted: true})
			return
		case protocol.ToolCreateSession:
			session.apps = arguments["apps"]
			session.generation = 1
			if session.onCreate != nil {
				session.onCreate()
			}
		case protocol.ToolUpdateSession:
			session.apps = arguments["desired"].(map[string]any)["apps"]
			session.generation++
		}
		name, _ := arguments["session"].(string)
		if name == "" {
			name = "gentle-otter-482"
		}
		status := "True"
		if tool == protocol.ToolGetSession && session.notReady > 0 {
			session.notReady--
			status = "False"
		}
		installationtest.WriteJSON(w, map[string]any{
			"name": name, "uid": "session-uid", "revision": "1", "generation": session.generation,
			"expiresAt": "2030-01-02T00:00:00Z", "apps": session.apps,
			"endpoints": []protocol.SessionEndpoint{{Name: "alpha", URL: "https://apps.example/mcp/alpha"}},
			"conditions": []map[string]any{{
				"type": protocol.ConditionReady, "status": status, "reason": "Ready", "observedGeneration": session.generation,
				"lastTransitionTime": session.transition.Format(time.RFC3339),
			}},
		})
	})
	h.start(t, build)
	return h, filepath.Join(root, manifestFileName)
}

func readTrace(t *testing.T, path string) *timingTrace {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	trace := new(timingTrace)
	if err := json.Unmarshal(data, trace); err != nil {
		t.Fatalf("trace is not one JSON document: %v\n%s", err, data)
	}
	return trace
}

func phaseNames(scope timingScope) []string {
	names := make([]string, 0, len(scope.Phases))
	for _, phase := range scope.Phases {
		names = append(names, phase.Name)
	}
	return names
}

func wantPhases(t *testing.T, scope timingScope, want ...string) {
	t.Helper()
	names := phaseNames(scope)
	for _, name := range want {
		if !slices.Contains(names, name) {
			t.Errorf("phases = %v, missing %q", names, name)
		}
	}
}

func TestTimingTraceCoversARun(t *testing.T) {
	for _, tc := range []struct {
		command string
		args    []string
		session string
		phases  []string
	}{
		{
			command: "dev", args: []string{"--detach", "--ttl", "1m"}, session: "gentle-otter-482",
			phases: []string{"create session", "type write", "type runtime install"},
		},
		{command: "deploy", session: protocol.ProductionName, phases: []string{"get session", "update session", "deploy"}},
	} {
		t.Run(tc.command, func(t *testing.T) {
			started := time.Date(2030, 1, 1, 0, 0, 10, 0, time.UTC)
			fc := testingclock.NewFakeClock(started)
			notReady := 1
			if tc.command == "deploy" {
				notReady = 2 // deploy reads the session once before it starts.
			}
			session := &timingSession{notReady: notReady, transition: started.Add(-7 * time.Second)}
			h, manifest := newTimingHarness(t, session, buildImage(t, "first"))
			h.run.Clock = fc
			path := filepath.Join(t.TempDir(), "trace.json")
			args := slices.Concat([]string{tc.command, "alpha", "--project", manifest, "--timing", path}, tc.args)
			if err := executeCommand(t.Context(), args, nil, nil, h.run); err != nil {
				t.Fatal(err)
			}
			trace := readTrace(t, path)
			if trace.Version != 1 || trace.Command != tc.command || trace.Session != tc.session || trace.Error != "" ||
				!slices.Equal(trace.Apps, []string{"alpha"}) || !slices.Equal(trace.Args, []string{"alpha"}) || trace.Origin != h.server.URL {
				t.Fatalf("trace context = %+v", trace)
			}
			if len(trace.Platforms) == 0 {
				t.Errorf("trace records no platforms")
			}
			if len(trace.Images) != 1 || trace.Images[0].App != "alpha" || !strings.HasPrefix(trace.Images[0].Digest, "sha256:") ||
				!strings.HasSuffix(trace.Images[0].Image, "@"+trace.Images[0].Digest) {
				t.Errorf("images = %+v", trace.Images)
			}
			wantPhases(t, trace.timingScope, slices.Concat(tc.phases, []string{
				"project config", "client config", "info fetch", "token", "graph resolution", "prepare contracts", "source snapshot",
				"tooling unpack", "source hashing", "contract extraction", "type generation", "upload grant",
				"stage types", "build and upload", "catalog push", "rollout",
			})...)
			if !slices.IsSortedFunc(trace.Phases, func(a, b timingPhase) int { return a.Start.Compare(b.Start) }) {
				t.Errorf("phases are not in start order: %v", phaseNames(trace.timingScope))
			}
			ready := trace.Ready
			if ready == nil || ready.PollLagMs == nil || *ready.PollLagMs != 7000 || ready.Polls != 2 ||
				ready.PreviousPollAt == nil || ready.Generation != ready.ObservedGeneration || trace.ReadyAfterMs == nil {
				t.Fatalf("ready = %+v, readyAfterMs = %v", ready, trace.ReadyAfterMs)
			}
		})
	}
}

func TestTimingFlagLeavesOutputUnchanged(t *testing.T) {
	for _, command := range [][]string{{"dev", "--detach", "--ttl", "1m"}, {"deploy"}} {
		t.Run(command[0], func(t *testing.T) {
			fc := testingclock.NewFakeClock(time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))
			h, manifest := newTimingHarness(t, &timingSession{transition: fc.Now()}, buildImage(t, "first"))
			h.run.Clock = fc
			run := func(extra ...string) (string, string) {
				var stdout, stderr bytes.Buffer
				args := slices.Concat(command, []string{"alpha", "--project", manifest, "-v"}, extra)
				if err := executeCommand(t.Context(), args, &stdout, &stderr, h.run); err != nil {
					t.Fatal(err)
				}
				return stdout.String(), stderr.String()
			}
			plainOut, plainErr := run()
			if !strings.Contains(plainErr, "Timing alpha build and upload") {
				t.Fatalf("existing timing lines missing: %q", plainErr)
			}
			if strings.Contains(plainErr, "graph resolution") {
				t.Fatalf("-v gained a timing line: %q", plainErr)
			}
			tracedOut, tracedErr := run("--timing", timingStderr)
			if tracedOut != plainOut {
				t.Fatalf("stdout = %q, want %q", tracedOut, plainOut)
			}
			rendered, document, found := strings.Cut(tracedErr, "{\n")
			if !found || rendered != plainErr {
				t.Fatalf("stderr before the trace = %q, want %q", rendered, plainErr)
			}
			trace := new(timingTrace)
			if err := json.Unmarshal([]byte("{\n"+document), trace); err != nil || trace.Command != command[0] {
				t.Fatalf("stderr trace = %+v, %v", trace, err)
			}
		})
	}
}

func TestTimingTraceRecordsEachEditCycle(t *testing.T) {
	marker := 0
	base := func(ctx context.Context, request buildRequest, progress io.Writer) (buildResult, error) {
		marker++
		return buildImage(t, strconv.Itoa(marker/2))(ctx, request, progress)
	}
	fc := testingclock.NewFakeClock(time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))
	session := &timingSession{transition: fc.Now().Add(-time.Second)}
	h, manifest := newTimingHarness(t, session, base)
	h.run.Clock = fc
	path := filepath.Join(t.TempDir(), "trace.json")
	stdout, stderr, stop := startDev(t, h.run, "alpha", "--project", manifest, "--timing", path)
	stepUntil(t, fc, devDebounce, func() bool { return strings.Contains(stdout.String(), "Watching for changes.") })
	source := map[string]string{"alpha/server.js": "export default {changed: true}"}

	root := filepath.Dir(manifest)
	started := len(stderr.String())
	sourceFiles(t, root, source)
	stepUntil(t, fc, devDebounce, func() bool { return strings.Contains(stderr.String()[started:], "remote update requested") })
	stepUntil(t, fc, workflowPoll, func() bool { return strings.Contains(stderr.String()[started:], "alpha ready") })

	started = len(stderr.String())
	sourceFiles(t, root, map[string]string{"alpha/server.js": "export default {changed: 2}"})
	stepUntil(t, fc, devDebounce, func() bool { return strings.Contains(stderr.String()[started:], "remote image is unchanged") })
	if err := stop(); err != nil {
		t.Fatalf("dev run: %v, stderr=%q", err, stderr.String())
	}

	trace := readTrace(t, path)
	if trace.Ready == nil || trace.ReadyAfterMs == nil {
		t.Fatalf("first readiness not recorded: %+v", trace.Ready)
	}
	wantPhases(t, trace.timingScope, "create session", "rollout", "delete session")
	if len(trace.Cycles) != 2 {
		t.Fatalf("cycles = %d, want 2", len(trace.Cycles))
	}
	updated, unchanged := trace.Cycles[0], trace.Cycles[1]
	if updated.Index != 1 || updated.Outcome != cycleReady || !slices.Equal(updated.Contexts, []string{filepath.Join(root, "alpha")}) ||
		updated.Ready == nil || updated.Ready.PollLagMs == nil || len(updated.Images) != 1 {
		t.Fatalf("updated cycle = %+v", updated)
	}
	wantPhases(t, updated.timingScope, "debounce", "contract extraction", "type generation", "build and upload", "update session", "rollout")
	debounce := updated.Phases[slices.IndexFunc(updated.Phases, func(p timingPhase) bool { return p.Name == "debounce" })]
	if debounce.DurationMs < float64(devDebounce.Milliseconds()) || debounce.Start != updated.Start {
		t.Errorf("debounce = %+v, cycle start = %v", debounce, updated.Start)
	}
	if updated.DurationMs != float64(updated.End.Sub(updated.Start).Milliseconds()) || updated.End.Before(debounce.End) {
		t.Errorf("cycle does not span its phases: %+v", updated)
	}
	if unchanged.Outcome != cycleUnchanged || slices.Contains(phaseNames(unchanged.timingScope), "update session") {
		t.Errorf("unchanged cycle = %+v", unchanged)
	}
	if names := phaseNames(trace.timingScope); slices.Contains(names, "debounce") {
		t.Errorf("cycle phases leaked into the run: %v", names)
	}
}

func TestTimingCycleOutcomes(t *testing.T) {
	failed := protocol.SessionResponse{Generation: 2}
	for _, tc := range []struct {
		name string
		run  func(*timingTrace) []string
	}{
		{name: "failed build", run: func(trace *timingTrace) []string {
			finish := trace.beginCycle(devChange{})
			finish(errors.New("build failed"))
			return []string{cycleFailed}
		}},
		{name: "failed rollout", run: func(trace *timingTrace) []string {
			finish := trace.beginCycle(devChange{})
			trace.awaitReady()
			finish(nil)
			trace.observe(failed, false)
			trace.rolloutEnded("ProgressDeadlineExceeded: unready")
			return []string{cycleFailed}
		}},
		{name: "exits while waiting", run: func(trace *timingTrace) []string {
			finish := trace.beginCycle(devChange{})
			trace.awaitReady()
			finish(nil)
			return []string{cycleUpdating}
		}},
		{name: "edit during a rollout", run: func(trace *timingTrace) []string {
			finish := trace.beginCycle(devChange{})
			trace.awaitReady()
			finish(nil)
			finish = trace.beginCycle(devChange{})
			trace.awaitReady()
			finish(nil)
			trace.observe(failed, true)
			trace.rolloutEnded("")
			return []string{cycleSuperseded, cycleReady}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			trace := newTimingTrace(testingclock.NewFakeClock(time.Unix(100, 0)), "dev", nil)
			want := tc.run(trace)
			var got []string
			for _, cycle := range trace.Cycles {
				got = append(got, cycle.Outcome)
			}
			if !slices.Equal(got, want) {
				t.Fatalf("outcomes = %v, want %v", got, want)
			}
		})
	}
}

func TestTimingTraceFailures(t *testing.T) {
	root := t.TempDir()
	sourceFiles(t, root, map[string]string{manifestFileName: "apps: [{unknown: true}]\n"})
	path := filepath.Join(root, "trace.json")
	config := commandConfig{Auth: ClientConfig{ConfigDir: t.TempDir()}}
	err := executeCommand(t.Context(), []string{"dev", "--project", filepath.Join(root, manifestFileName), "--timing", path}, nil, nil, config)
	if err == nil {
		t.Fatal("invalid project accepted")
	}
	if trace := readTrace(t, path); trace.Error == "" || !slices.Contains(phaseNames(trace.timingScope), "project config") {
		t.Fatalf("failed run trace = %+v", trace)
	}

	h, manifest := newTimingHarness(t, &timingSession{}, buildImage(t, "first"))
	missing := filepath.Join(root, "missing", "trace.json")
	err = executeCommand(t.Context(), []string{"deploy", "alpha", "--project", manifest, "--timing", missing}, nil, nil, h.run)
	if err == nil || !strings.Contains(err.Error(), "write timing trace") {
		t.Fatalf("unwritable trace = %v", err)
	}
}

func TestProgressMeasureRecordsWithoutPrinting(t *testing.T) {
	for _, verbose := range []bool{false, true} {
		fc := testingclock.NewFakeClock(time.Unix(100, 0))
		var stream bytes.Buffer
		output, _, _ := newProgressOutput(io.Discard, &stream, verbose, fc)
		output.measure("alpha", "untraced")()
		output.trace = newTimingTrace(fc, "dev", nil)
		finish := output.measure("alpha", "traced")
		fc.Step(1500 * time.Millisecond)
		finish()
		output.timing().measure("alpha", "trace only")()
		want := ""
		if verbose {
			want = "Timing alpha untraced: 0s\nTiming alpha traced: 1.5s\n"
		}
		if stream.String() != want {
			t.Fatalf("verbose=%v: printed %q, want %q", verbose, stream.String(), want)
		}
		if names := phaseNames(output.trace.timingScope); !slices.Equal(names, []string{"traced", "trace only"}) {
			t.Fatalf("recorded %v", names)
		}
		if phase := output.trace.Phases[0]; phase.DurationMs != 1500 || phase.Subject != "alpha" {
			t.Fatalf("phase = %+v", phase)
		}
	}
	var nothing *progressOutput
	nothing.measure("alpha", "nil output")()
	nothing.timing().measure("alpha", "nil trace")()
}

func TestSolveStatusesSeparatesBuilderPhases(t *testing.T) {
	fc := testingclock.NewFakeClock(time.Unix(100, 0))
	trace := newTimingTrace(fc, "dev", nil)
	display := make(chan *client.SolveStatus)
	statuses := trace.solveStatuses("alpha linux/arm64", display)
	var forwarded int
	var rendered sync.WaitGroup
	rendered.Go(func() {
		for range display {
			forwarded++
		}
	})
	builderClock := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)
	vertex := func(name string, took time.Duration, finished bool) *client.Vertex {
		result := &client.Vertex{Digest: digest.FromString(name), Name: name, Started: &builderClock}
		if finished {
			result.Completed = new(builderClock.Add(took))
		}
		return result
	}
	for _, status := range []*client.SolveStatus{
		{Vertexes: []*client.Vertex{vertex("exporting to oci image format", 0, false)}},
		{Vertexes: []*client.Vertex{vertex("[internal] load build context", 250*time.Millisecond, true), vertex("[1/2] RUN true", time.Hour, true)}},
		{Vertexes: []*client.Vertex{vertex("exporting to oci image format", 3*time.Second, true)}},
		{Vertexes: []*client.Vertex{vertex("exporting to oci image format", 3*time.Second, true)}},
	} {
		statuses <- status
	}
	close(statuses)
	rendered.Wait()
	if forwarded != 4 {
		t.Fatalf("forwarded %d statuses to the display, want 4", forwarded)
	}
	got := map[string]float64{}
	for _, phase := range trace.Phases {
		if phase.Subject != "alpha linux/arm64" || !phase.End.Equal(fc.Now()) {
			t.Errorf("phase = %+v", phase)
		}
		got[phase.Name] = phase.DurationMs
	}
	if len(trace.Phases) != 2 || got["context upload"] != 250 || got["export"] != 3000 {
		t.Fatalf("phases = %+v", trace.Phases)
	}
	var untraced *timingTrace
	if channel := make(chan *client.SolveStatus); untraced.solveStatuses("alpha", channel) != channel {
		t.Fatal("an untraced solve must report straight to the display")
	}
}

func TestRegistryClientCountsBlobs(t *testing.T) {
	_, registry := buildRegistry(t)
	host := strings.TrimPrefix(registry.URL, "https://")
	blob := []byte("layer contents")
	descriptor := content.NewDescriptorFromBytes(ocispec.MediaTypeImageLayer, blob)
	session := func(trace *timingTrace) func(bool) bool {
		repository, err := imageRepository(host+"/holon/upload/alpha", "grant-secret", trace.registryClient(registry.Client()))
		if err != nil {
			t.Fatal(err)
		}
		return func(push bool) bool {
			if push {
				if err := repository.Push(t.Context(), descriptor, bytes.NewReader(blob)); err != nil {
					t.Fatal(err)
				}
				return true
			}
			exists, err := repository.Exists(t.Context(), descriptor)
			if err != nil {
				t.Fatal(err)
			}
			return exists
		}
	}
	clk := testingclock.NewFakeClock(time.Unix(100, 0))
	first := newTimingTrace(clk, "dev", nil)
	act := session(first)
	if act(false) {
		t.Fatal("fresh registry already has the blob")
	}
	act(true)
	act(false)
	first.finish()
	if want := (timingBlobs{Pushed: 1, BytesPushed: int64(len(blob))}); first.Blobs != want {
		t.Fatalf("pushed run = %+v, want %+v", first.Blobs, want)
	}

	second := newTimingTrace(clk, "dev", nil)
	act = session(second)
	for range 2 {
		if !act(false) {
			t.Fatal("pushed blob is missing")
		}
	}
	second.finish()
	if want := (timingBlobs{Skipped: 1, BytesSkipped: int64(len(blob))}); second.Blobs != want {
		t.Fatalf("skipping run = %+v, want %+v", second.Blobs, want)
	}
}

func TestRegistryExchangeClassification(t *testing.T) {
	const layer = "sha256:" + "ab"
	for _, tc := range []struct {
		name   string
		method string
		target string
		status int
		size   int64
		want   timingBlobs
	}{
		{name: "rejected upload", method: http.MethodPut, target: "/v2/a/b/blobs/uploads/1?digest=" + layer, status: 401, size: 9},
		{name: "cross-repository mount", method: http.MethodPost, target: "/v2/a/blobs/uploads/?mount=" + layer + "&from=b", status: 201, want: timingBlobs{Mounted: 1}},
		{name: "mount the registry declined", method: http.MethodPost, target: "/v2/a/blobs/uploads/?mount=" + layer + "&from=b", status: 202},
		{name: "manifest", method: http.MethodPut, target: "/v2/a/b/manifests/sha256:ab", status: 201, size: 9, want: timingBlobs{Manifests: 1, ManifestBytes: 9}},
		{name: "existing manifest is not a blob", method: http.MethodHead, target: "/v2/a/manifests/sha256:ab", status: 200, size: 9},
		{name: "missing blob", method: http.MethodHead, target: "/v2/a/blobs/" + layer, status: 404, size: 9},
		{name: "existing blob", method: http.MethodHead, target: "/v2/a/b/blobs/" + layer, status: 200, size: 9, want: timingBlobs{Skipped: 1, BytesSkipped: 9}},
		{name: "token request", method: http.MethodPost, target: "/token", status: 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request, err := http.NewRequest(tc.method, "https://registry.example"+tc.target, nil)
			if err != nil {
				t.Fatal(err)
			}
			request.ContentLength = tc.size
			var scope timingScope
			scope.registryExchange(request, &http.Response{StatusCode: tc.status, ContentLength: tc.size})
			scope.finish()
			if scope.Blobs != tc.want {
				t.Fatalf("blobs = %+v, want %+v", scope.Blobs, tc.want)
			}
		})
	}
}

func TestTimingPollLag(t *testing.T) {
	observedAt := time.Date(2030, 1, 1, 0, 0, 10, 500_000_000, time.UTC)
	session := func(transition time.Time) protocol.SessionResponse {
		return protocol.SessionResponse{Generation: 3, Conditions: []protocol.Condition{{
			Type: protocol.ConditionReady, Status: "True", ObservedGeneration: 2, LastTransitionTime: protocol.Time(transition),
		}}}
	}
	for _, tc := range []struct {
		name       string
		transition time.Time
		want       *float64
	}{
		{name: "after the transition", transition: observedAt.Add(-2500 * time.Millisecond), want: new(2500.0)},
		{name: "server clock ahead", transition: observedAt.Add(time.Second), want: new(-1000.0)},
		{name: "no transition time"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fc := testingclock.NewFakeClock(observedAt.Add(-time.Second))
			trace := newTimingTrace(fc, "dev", nil)
			trace.awaitReady()
			trace.observe(protocol.SessionResponse{}, false)
			lastNotReady := fc.Now()
			fc.SetTime(observedAt)
			trace.observe(session(tc.transition), true)
			fc.Step(time.Minute)
			trace.observe(session(tc.transition), true)

			ready := trace.Ready
			if ready.Polls != 2 || !ready.ObservedAt.Equal(observedAt) || !ready.PreviousPollAt.Equal(lastNotReady) {
				t.Fatalf("a later look replaced the first observation: %+v", ready)
			}
			if ready.Generation != 3 || ready.ObservedGeneration != 2 {
				t.Fatalf("generations = %d, %d; a stale condition must stay visible", ready.Generation, ready.ObservedGeneration)
			}
			if (ready.PollLagMs == nil) != (tc.want == nil) || tc.want != nil && *ready.PollLagMs != *tc.want {
				t.Fatalf("pollLagMs = %v, want %v", ready.PollLagMs, tc.want)
			}
		})
	}
}

func TestTimingTraceDocumentShape(t *testing.T) {
	fc := testingclock.NewFakeClock(time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))
	trace := newTimingTrace(fc, "dev", []string{"chat"})
	trace.setOrigin("https://holon.example")
	trace.setSession("gentle-otter-482")
	trace.addPlatforms([]string{"linux/arm64"})
	trace.addApps([]protocol.App{{Name: "chat", Image: "registry.example/chat@sha256:ab"}})
	ready := protocol.SessionResponse{Conditions: []protocol.Condition{{
		Type: protocol.ConditionReady, Status: "True", LastTransitionTime: protocol.Time(fc.Now()),
	}}}
	trace.awaitReady()
	trace.observe(protocol.SessionResponse{}, false)
	trace.observe(ready, true)
	trace.rolloutEnded("")
	finish := trace.beginCycle(devChange{manifest: true})
	trace.addApps([]protocol.App{{Name: "chat", Image: "registry.example/chat@sha256:cd"}})
	trace.awaitReady()
	finish(nil)
	trace.observe(protocol.SessionResponse{}, false)
	trace.observe(ready, true)
	trace.rolloutEnded("")

	var stderr bytes.Buffer
	if err := trace.write(timingStderr, &stderr, nil); err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(stderr.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	wantKeys := func(name string, value any, keys ...string) {
		t.Helper()
		object, ok := value.(map[string]any)
		if !ok {
			t.Fatalf("%s = %v", name, value)
		}
		for _, key := range keys {
			if _, ok := object[key]; !ok {
				t.Errorf("%s has no %q: %v", name, key, object)
			}
		}
	}
	wantKeys("document", document, "version", "command", "args", "origin", "session", "apps", "platforms",
		"start", "end", "durationMs", "readyAfterMs", "phases", "images", "blobs", "ready", "cycles")
	wantKeys("image", document["images"].([]any)[0], "app", "image", "digest")
	wantKeys("blobs", document["blobs"], "pushed", "mounted", "skipped", "manifests", "bytesPushed", "bytesSkipped", "manifestBytes")
	wantKeys("ready", document["ready"], "generation", "observedGeneration", "lastTransitionTime", "observedAt", "previousPollAt", "polls", "pollLagMs")
	cycle := document["cycles"].([]any)[0]
	wantKeys("cycle", cycle, "index", "manifest", "start", "end", "durationMs", "outcome", "phases", "images", "blobs", "ready")
}

func TestTimingTraceMarshalsItsFieldsOnce(t *testing.T) {
	fc := testingclock.NewFakeClock(time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))
	trace := newTimingTrace(fc, "dev", []string{"chat"})
	trace.measure("chat", "plan")()
	var stderr bytes.Buffer
	if err := trace.write(timingStderr, &stderr, errors.New("failed")); err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(stderr.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"version", "command", "args", "start", "end", "durationMs", "error", "phases", "blobs"} {
		if _, ok := document[key]; !ok {
			t.Errorf("document has no %q: %s", key, fmt.Sprint(document))
		}
	}
	phase := document["phases"].([]any)[0].(map[string]any)
	for _, key := range []string{"name", "subject", "start", "end", "durationMs"} {
		if _, ok := phase[key]; !ok {
			t.Errorf("phase has no %q: %v", key, phase)
		}
	}
}
