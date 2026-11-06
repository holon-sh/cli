package installation

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/moby/buildkit/client"
	"github.com/opencontainers/go-digest"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/clock"
	"oras.land/oras-go/v2/registry/remote/retry"

	"github.com/holon-sh/protocol"
)

// timingStderr is the --timing path that selects standard error.
const timingStderr = "-"

// processStarted is the earliest moment this process can observe; it excludes the dynamic loader.
var processStarted = time.Now()

// timingTrace is the document --timing writes at exit: every phase of one holon dev or holon deploy,
// and the context a later comparison needs. A nil trace records nothing, which is what keeps a run
// without the flag unchanged. Phases nest and run concurrently, so their durations do not sum to the
// total; start and end place each one on the run's timeline.
type timingTrace struct {
	Version      int       `json:"version"`
	Command      string    `json:"command"`
	Args         []string  `json:"args,omitempty"`
	Origin       string    `json:"origin,omitempty"`
	Session      string    `json:"session,omitempty"`
	Apps         []string  `json:"apps,omitempty"`
	Platforms    []string  `json:"platforms,omitempty"`
	Start        time.Time `json:"start"`
	End          time.Time `json:"end"`
	DurationMs   float64   `json:"durationMs"`
	ReadyAfterMs *float64  `json:"readyAfterMs,omitempty"`
	Error        string    `json:"error,omitempty"`
	timingScope
	Cycles []*timingCycle `json:"cycles,omitempty"`

	mu           sync.Mutex
	clock        clock.PassiveClock
	current      *timingCycle
	awaiting     *timingCycle
	rolloutStart time.Time
	readyAt      time.Time
	polls        int
	lastPoll     time.Time
}

// timingScope is what one run, or one edit cycle within it, did.
type timingScope struct {
	Phases []timingPhase `json:"phases"`
	Images []timingImage `json:"images,omitempty"`
	Blobs  timingBlobs   `json:"blobs"`
	Ready  *timingReady  `json:"ready,omitempty"`

	existing    map[string]int64
	transferred sets.Set[string]
}

type timingPhase struct {
	Name       string    `json:"name"`
	Subject    string    `json:"subject,omitempty"`
	Start      time.Time `json:"start"`
	End        time.Time `json:"end"`
	DurationMs float64   `json:"durationMs"`
}

type timingImage struct {
	App    string `json:"app"`
	Image  string `json:"image"`
	Digest string `json:"digest,omitempty"`
}

// timingBlobs counts registry traffic as the registry acknowledged it. A blob is skipped when the
// registry already had it and nothing was sent; a mounted blob moved between repositories without
// its bytes. Bytes are what crossed the wire, so a blob pushed to two repositories counts twice.
type timingBlobs struct {
	Pushed        int   `json:"pushed"`
	Mounted       int   `json:"mounted"`
	Skipped       int   `json:"skipped"`
	Manifests     int   `json:"manifests"`
	BytesPushed   int64 `json:"bytesPushed"`
	BytesSkipped  int64 `json:"bytesSkipped"`
	ManifestBytes int64 `json:"manifestBytes"`
}

// timingReady compares when the Session became Ready with when the CLI saw it. lastTransitionTime has
// one-second resolution and the server's clock, so pollLagMs is only that accurate. previousPollAt is
// the last time this clock saw the Session not ready: the gap from it to observedAt bounds the lag
// with neither limitation.
type timingReady struct {
	Generation         int64      `json:"generation"`
	ObservedGeneration int64      `json:"observedGeneration"`
	LastTransitionTime *time.Time `json:"lastTransitionTime,omitempty"`
	ObservedAt         time.Time  `json:"observedAt"`
	PreviousPollAt     *time.Time `json:"previousPollAt,omitempty"`
	Polls              int        `json:"polls"`
	PollLagMs          *float64   `json:"pollLagMs,omitempty"`
}

// timingCycle is one edit in the dev loop, from the first file event to the Session being ready again.
type timingCycle struct {
	Index      int       `json:"index"`
	Contexts   []string  `json:"contexts,omitempty"`
	Manifest   bool      `json:"manifest,omitempty"`
	Start      time.Time `json:"start"`
	End        time.Time `json:"end"`
	DurationMs float64   `json:"durationMs"`
	Outcome    string    `json:"outcome"`
	Error      string    `json:"error,omitempty"`
	timingScope
}

const (
	cycleUnchanged  = "unchanged"
	cycleFailed     = "failed"
	cycleUpdating   = "updating"
	cycleReady      = "ready"
	cycleSuperseded = "superseded"
)

func milliseconds(duration time.Duration) float64 {
	return float64(duration.Microseconds()) / 1000
}

func newTimingTrace(clk clock.PassiveClock, command string, args []string) *timingTrace {
	now := clk.Now()
	trace := &timingTrace{Version: 1, Command: command, Args: args, Start: now, clock: clk}
	if _, real := clk.(clock.RealClock); real {
		trace.Start = processStarted
		trace.add("", "process startup", processStarted, now)
	}
	return trace
}

func (o *progressOutput) timing() *timingTrace {
	if o == nil {
		return nil
	}
	return o.trace
}

// measure starts a phase and returns the function that ends it. Phases recorded during an edit cycle
// belong to that cycle.
func (t *timingTrace) measure(subject, name string) func() {
	if t == nil {
		return func() {}
	}
	started := t.clock.Now()
	return func() { t.add(subject, name, started, t.clock.Now()) }
}

func (t *timingTrace) add(subject, name string, start, end time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.scope().addPhase(subject, name, start, end)
}

func (t *timingTrace) scope() *timingScope {
	if t.current != nil {
		return &t.current.timingScope
	}
	return &t.timingScope
}

func (s *timingScope) addPhase(subject, name string, start, end time.Time) {
	s.Phases = append(s.Phases, timingPhase{
		Name: name, Subject: subject, Start: start, End: end, DurationMs: milliseconds(end.Sub(start)),
	})
}

func (t *timingTrace) setOrigin(origin string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Origin = origin
}

func (t *timingTrace) setSession(name string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Session = name
}

func (t *timingTrace) addPlatforms(platforms []string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, platform := range platforms {
		if !slices.Contains(t.Platforms, platform) {
			t.Platforms = append(t.Platforms, platform)
		}
	}
}

// addApps records what was built: the run's app names, and the images of those that have one.
func (t *timingTrace) addApps(apps []protocol.App) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	scope := t.scope()
	for _, app := range apps {
		if !slices.Contains(t.Apps, app.Name) {
			t.Apps = append(t.Apps, app.Name)
		}
		if app.Image != "" {
			_, imageDigest, _ := strings.Cut(app.Image, "@")
			scope.Images = append(scope.Images, timingImage{App: app.Name, Image: app.Image, Digest: imageDigest})
		}
	}
}

func (t *timingTrace) beginCycle(change devChange) func(error) {
	if t == nil {
		return func(error) {}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.clock.Now()
	first := change.firstEvent
	if first.IsZero() {
		first = now
	}
	cycle := &timingCycle{
		Index: len(t.Cycles) + 1, Contexts: sets.List(change.contexts), Manifest: change.manifest,
		Start: first, Outcome: cycleUnchanged,
	}
	cycle.addPhase("", "debounce", first, now)
	t.Cycles = append(t.Cycles, cycle)
	t.current = cycle
	return func(err error) {
		t.mu.Lock()
		defer t.mu.Unlock()
		t.current = nil
		cycle.finish(t.clock.Now())
		if err != nil {
			cycle.Outcome, cycle.Error = cycleFailed, err.Error()
		}
	}
}

func (c *timingCycle) finish(end time.Time) {
	c.End, c.DurationMs = end, milliseconds(end.Sub(c.Start))
}

// awaitReady marks the Session as rolling out: from here the next Ready observation belongs to the
// edit cycle in progress, or to the run when there is none. An edit still waiting is superseded.
func (t *timingTrace) awaitReady() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.awaiting != nil {
		t.awaiting.Outcome = cycleSuperseded
	}
	t.awaiting = t.current
	if t.current != nil {
		t.current.Outcome = cycleUpdating
	}
	t.rolloutStart, t.polls, t.lastPoll, t.readyAt = t.clock.Now(), 0, time.Time{}, time.Time{}
}

// observe records one look at the Session. The first look that finds it ready records the poll lag.
func (t *timingTrace) observe(session protocol.SessionResponse, ready bool) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.clock.Now()
	t.polls++
	if !ready {
		t.lastPoll = now
		return
	}
	if !t.readyAt.IsZero() {
		return
	}
	t.readyAt = now
	condition := session.Condition(protocol.ConditionReady)
	record := &timingReady{
		Generation: session.Generation, ObservedGeneration: condition.ObservedGeneration, ObservedAt: now, Polls: t.polls,
	}
	if transition := time.Time(condition.LastTransitionTime); !transition.IsZero() {
		record.LastTransitionTime = &transition
		record.PollLagMs = new(milliseconds(now.Sub(transition)))
	}
	if !t.lastPoll.IsZero() {
		record.PreviousPollAt = new(t.lastPoll)
	}
	if t.awaiting != nil {
		t.awaiting.Ready = record
		return
	}
	t.Ready = record
	if t.ReadyAfterMs == nil {
		t.ReadyAfterMs = new(milliseconds(now.Sub(t.Start)))
	}
}

// rolloutEnded closes the wait awaitReady began, for the dev loop, whose wait is not one function
// call that measure can bracket. A failed rollout passes the reason.
func (t *timingTrace) rolloutEnded(failure string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.rolloutStart.IsZero() {
		return
	}
	end := t.clock.Now()
	if !t.readyAt.IsZero() {
		end = t.readyAt
	}
	scope := &t.timingScope
	if cycle := t.awaiting; cycle != nil {
		scope = &cycle.timingScope
		cycle.finish(end)
		cycle.Outcome, cycle.Error = cycleReady, failure
		if failure != "" {
			cycle.Outcome = cycleFailed
		}
	}
	scope.addPhase("", "rollout", t.rolloutStart, end)
	t.awaiting, t.rolloutStart = nil, time.Time{}
}

// solveStatuses returns the channel to give a BuildKit solve, forwarding to display and turning the
// vertices that have a phase of their own into phases of subject. It takes each duration from the
// builder, whose clock this one need not share, and ends it when the status arrived.
func (t *timingTrace) solveStatuses(subject string, display chan *client.SolveStatus) chan *client.SolveStatus {
	if t == nil {
		return display
	}
	statuses := make(chan *client.SolveStatus)
	go func() {
		defer close(display)
		done := sets.New[digest.Digest]()
		for status := range statuses {
			for _, vertex := range status.Vertexes {
				name := solvePhase(vertex.Name)
				if name == "" || vertex.Started == nil || vertex.Completed == nil || done.Has(vertex.Digest) {
					continue
				}
				done.Insert(vertex.Digest)
				end := t.clock.Now()
				t.add(subject, name, end.Add(-vertex.Completed.Sub(*vertex.Started)), end)
			}
			display <- status
		}
	}()
	return statuses
}

func solvePhase(vertex string) string {
	switch {
	case strings.HasPrefix(vertex, "exporting to"):
		return "export"
	case vertex == "local://context" || strings.Contains(vertex, "load build context"):
		return "context upload"
	}
	return ""
}

var (
	registryBlob     = regexp.MustCompile(`^/v2/.+/blobs/(sha256:[0-9a-f]+)$`)
	registryManifest = regexp.MustCompile(`^/v2/.+/manifests/[^/]+$`)
)

type timingTransport struct {
	base  http.RoundTripper
	trace *timingTrace
}

func (t *timingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := t.base.RoundTrip(request)
	if err == nil {
		t.trace.registryExchange(request, response)
	}
	return response, err
}

// registryClient returns base counting the registry requests it makes. Without a base it starts from
// the client imageRepository defaults to.
func (t *timingTrace) registryClient(base *http.Client) *http.Client {
	counted := *cmp.Or(base, retry.DefaultClient)
	counted.Transport = &timingTransport{base: cmp.Or(counted.Transport, http.DefaultTransport), trace: t}
	return &counted
}

func (t *timingTrace) registryExchange(request *http.Request, response *http.Response) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.scope().registryExchange(request, response)
}

func (s *timingScope) registryExchange(request *http.Request, response *http.Response) {
	if s.existing == nil {
		s.existing, s.transferred = map[string]int64{}, sets.New[string]()
	}
	query := request.URL.Query()
	accepted := response.StatusCode >= 200 && response.StatusCode < 300
	switch {
	case request.Method == http.MethodPut && accepted && query.Get("digest") != "":
		s.Blobs.Pushed++
		s.Blobs.BytesPushed += request.ContentLength
		s.transferred.Insert(query.Get("digest"))
		delete(s.existing, query.Get("digest"))
	case request.Method == http.MethodPost && response.StatusCode == http.StatusCreated && query.Get("mount") != "":
		s.Blobs.Mounted++
		s.transferred.Insert(query.Get("mount"))
		delete(s.existing, query.Get("mount"))
	case request.Method == http.MethodPut && accepted && registryManifest.MatchString(request.URL.Path):
		s.Blobs.Manifests++
		s.Blobs.ManifestBytes += request.ContentLength
	case request.Method == http.MethodHead && accepted:
		if match := registryBlob.FindStringSubmatch(request.URL.Path); match != nil && !s.transferred.Has(match[1]) {
			s.existing[match[1]] = response.ContentLength
		}
	}
}

func (s *timingScope) finish() {
	slices.SortStableFunc(s.Phases, func(a, b timingPhase) int { return a.Start.Compare(b.Start) })
	s.Blobs.Skipped, s.Blobs.BytesSkipped = len(s.existing), 0
	for _, size := range s.existing {
		s.Blobs.BytesSkipped += size
	}
}

// write ends the trace and writes it to path, or to stderr for "-".
func (t *timingTrace) write(path string, stderr io.Writer, result error) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.End = t.clock.Now()
	t.DurationMs = milliseconds(t.End.Sub(t.Start))
	if result != nil {
		t.Error = result.Error()
	}
	t.finish()
	for _, cycle := range t.Cycles {
		cycle.timingScope.finish()
	}
	data, err := json.MarshalIndent(t, "", "  ")
	if err == nil {
		data = append(data, '\n')
		if path == timingStderr {
			_, err = stderr.Write(data)
		} else {
			err = os.WriteFile(path, data, 0o644)
		}
	}
	if err != nil {
		return fmt.Errorf("write timing trace: %w", err)
	}
	return nil
}

// withTiming adds --timing. It wraps the whole command, so the trace starts before the project file is
// read and is written after the progress display has stopped.
func withTiming(cmd *cobra.Command, config *commandConfig) *cobra.Command {
	var path string
	cmd.Flags().StringVar(&path, "timing", "", "Write a JSON trace to `FILE` at exit (- for stderr): each phase's start, end and duration, "+
		"image digests, platforms, blobs pushed and skipped, Ready poll lag, and one entry per edit in holon dev")
	preRun, run := cmd.PreRunE, cmd.RunE
	cmd.PreRunE = func(cmd *cobra.Command, args []string) error {
		if path == "" {
			return preRun(cmd, args)
		}
		config.trace = newTimingTrace(config.clock(), cmd.Name(), args)
		config.RegistryHTTPClient = config.trace.registryClient(config.RegistryHTTPClient)
		finish := config.trace.measure("", "project config")
		err := preRun(cmd, args)
		finish()
		if err != nil {
			return errors.Join(err, config.trace.write(path, cmd.ErrOrStderr(), err))
		}
		return nil
	}
	cmd.RunE = func(cmd *cobra.Command, args []string) (result error) {
		if path == "" {
			return run(cmd, args)
		}
		stderr := cmd.ErrOrStderr()
		defer func() { result = errors.Join(result, config.trace.write(path, stderr, result)) }()
		return run(cmd, args)
	}
	return cmd
}
