package installation

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	testingclock "k8s.io/utils/clock/testing"

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
	failing    bool
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
		case protocol.ToolUpdateSession:
			session.apps = arguments["desired"].(map[string]any)["apps"]
			session.generation++
		}
		name, _ := arguments["session"].(string)
		if name == "" {
			name = "gentle-otter-482"
		}
		status, reason := "True", "Ready"
		switch {
		case session.failing && tool == protocol.ToolUpdateSession:
			status, reason = "False", "Progressing"
		case session.failing && tool == protocol.ToolGetSession:
			status, reason = "False", protocol.ReasonProgressDeadline
		case tool == protocol.ToolGetSession && session.notReady > 0:
			session.notReady--
			status = "False"
		}
		installationtest.WriteJSON(w, map[string]any{
			"name": name, "uid": "session-uid", "revision": "1", "generation": session.generation,
			"expiresAt": "2030-01-02T00:00:00Z", "apps": session.apps,
			"endpoints": []protocol.SessionEndpoint{{Name: "alpha", URL: "https://apps.example/mcp/alpha"}},
			"conditions": []map[string]any{{
				"type": protocol.ConditionReady, "status": status, "reason": reason, "observedGeneration": session.generation,
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

func phaseNames(phases []timingPhase) []string {
	names := make([]string, 0, len(phases))
	for _, phase := range phases {
		names = append(names, phase.Name)
	}
	return names
}

func wantPhases(t *testing.T, phases []timingPhase, want ...string) {
	t.Helper()
	names := phaseNames(phases)
	for _, name := range want {
		if !slices.Contains(names, name) {
			t.Errorf("phases = %v, missing %q", names, name)
		}
	}
}

func TestTimingTrace(t *testing.T) {
	for _, tc := range []struct {
		name     string
		args     []string
		notReady int
		project  string
		path     string
		err      string
		phases   []string
	}{
		{
			name: "dev", args: []string{"dev", "--detach", "--ttl", "1m"}, notReady: 1,
			phases: []string{"create session", "type write", "type runtime install"},
		},
		// deploy reads the session once before it starts.
		{name: "deploy", args: []string{"deploy"}, notReady: 2, phases: []string{"get session", "update session", "deploy"}},
		{name: "invalid project", args: []string{"dev"}, project: "apps: [{unknown: true}]\n", err: "unknown"},
		{name: "unwritable trace", args: []string{"deploy"}, path: filepath.Join("missing", "trace.json"), err: "write timing trace"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			started := time.Date(2030, 1, 1, 0, 0, 10, 0, time.UTC)
			fc := testingclock.NewFakeClock(started)
			session := &timingSession{notReady: tc.notReady, transition: started.Add(-7 * time.Second)}
			h, manifest := newTimingHarness(t, session, buildImage(t, "first"))
			h.run.Clock = fc
			if tc.project != "" {
				manifest = filepath.Join(t.TempDir(), manifestFileName)
				sourceFiles(t, filepath.Dir(manifest), map[string]string{manifestFileName: tc.project})
			}
			path := filepath.Join(t.TempDir(), cmp.Or(tc.path, "trace.json"))
			args := slices.Concat(tc.args, []string{"alpha", "--project", manifest, "--timing", path})
			err := executeCommand(t.Context(), args, nil, nil, h.run)
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("error = %v, want %q", err, tc.err)
				}
				if tc.path != "" {
					return
				}
				if trace := readTrace(t, path); trace.Error == "" || !slices.Contains(phaseNames(trace.Phases), "project config") {
					t.Fatalf("failed run trace = %+v", trace)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			trace := readTrace(t, path)
			if trace.Command != tc.args[0] || trace.Error != "" {
				t.Fatalf("trace = %+v", trace)
			}
			if len(trace.Images) != 1 || trace.Images[0].App != "alpha" || !strings.Contains(trace.Images[0].Image, "@sha256:") {
				t.Errorf("images = %+v", trace.Images)
			}
			wantPhases(t, trace.Phases, slices.Concat(tc.phases, []string{
				"project config", "client", "token", "graph resolution", "prepare contracts", "source snapshot",
				"source hashing", "contract extraction", "type generation", "upload grant",
				"stage types", "build and upload", "catalog push", "rollout",
			})...)
			if !slices.IsSortedFunc(trace.Phases, func(a, b timingPhase) int { return a.Start.Compare(b.Start) }) {
				t.Errorf("phases are not in start order: %v", phaseNames(trace.Phases))
			}
			if len(trace.Ready) != 1 || trace.Ready[0].PollLagMs != 7000 {
				t.Fatalf("ready = %+v", trace.Ready)
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
				args := slices.Concat(command, []string{"alpha", "--project", manifest}, extra)
				if err := executeCommand(t.Context(), args, &stdout, &stderr, h.run); err != nil {
					t.Fatal(err)
				}
				return stdout.String(), stderr.String()
			}
			plainOut, plainErr := run()
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
	var image atomic.Int64
	build := func(ctx context.Context, request buildRequest, progress io.Writer) (buildResult, error) {
		return buildImage(t, strconv.FormatInt(image.Load(), 10))(ctx, request, progress)
	}
	fc := testingclock.NewFakeClock(time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))
	session := &timingSession{transition: fc.Now().Add(-time.Second)}
	h, manifest := newTimingHarness(t, session, build)
	h.run.Clock = fc
	path := filepath.Join(t.TempDir(), "trace.json")
	stdout, stderr, stop := startDev(t, h.run, "alpha", "--project", manifest, "--timing", path)
	stepUntil(t, fc, devDebounce, func() bool { return strings.Contains(stdout.String(), "Watching for changes.") })
	changed := filepath.Join(filepath.Dir(manifest), "alpha", "server.js")
	edit := func(version int64, settled string) {
		image.Store(version)
		started := len(stderr.String())
		sourceFiles(t, filepath.Dir(manifest), map[string]string{"alpha/server.js": "export default {version: " + strconv.FormatInt(version, 10) + "}"})
		stepUntil(t, fc, devDebounce, func() bool { return strings.Contains(stderr.String()[started:], "remote update requested") })
		stepUntil(t, fc, workflowPoll, func() bool { return strings.Contains(stderr.String()[started:], settled) })
	}
	edit(1, "alpha ready")
	session.mu.Lock()
	session.failing = true
	session.mu.Unlock()
	edit(2, "alpha deployment failed")
	if err := stop(); err != nil {
		t.Fatalf("dev run: %v, stderr=%q", err, stderr.String())
	}

	trace := readTrace(t, path)
	wantPhases(t, trace.Phases, "create session", "update session", "delete session")
	var edits []timingPhase
	for _, phase := range trace.Phases {
		if phase.Name == "edit" {
			edits = append(edits, phase)
		}
	}
	if len(edits) != 2 || edits[0].Subject != changed || edits[0].Failed || edits[1].Subject != changed || !edits[1].Failed {
		t.Fatalf("edits = %+v, want %s ready then failed", edits, changed)
	}
	if edits[0].DurationMs < float64(devDebounce.Milliseconds()) {
		t.Errorf("edit does not span the debounce: %+v", edits[0])
	}
	update := trace.Phases[slices.IndexFunc(trace.Phases, func(p timingPhase) bool { return p.Name == "update session" })]
	if update.Start.Before(edits[0].Start) || update.End.After(edits[0].End) {
		t.Errorf("update session %+v lies outside its edit %+v", update, edits[0])
	}
	if len(trace.Ready) != 2 || !trace.Ready[1].ObservedAt.Equal(edits[0].End) {
		t.Errorf("ready = %+v, want the session and the first edit", trace.Ready)
	}
}
