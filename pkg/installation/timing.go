package installation

import (
	"cmp"
	"encoding/json"
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
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/clock"
	"oras.land/oras-go/v2/registry/remote/retry"

	"github.com/holon-sh/protocol"
)

// timingStderr is the --timing path that selects standard error.
const timingStderr = "-"

// timingTrace is the document --timing writes at exit: every phase of one holon dev or holon deploy on
// one timeline. A nil trace records nothing, which is what keeps a run without the flag unchanged.
// Phases nest and run concurrently, so their durations do not sum to the total; start and end place
// each one, and nesting is read from the timeline. Each edit in holon dev is an "edit" phase.
type timingTrace struct {
	Command    string        `json:"command"`
	Start      time.Time     `json:"start"`
	End        time.Time     `json:"end"`
	DurationMs float64       `json:"durationMs"`
	Error      string        `json:"error,omitempty"`
	Phases     []timingPhase `json:"phases"`
	Images     []timingImage `json:"images,omitempty"`
	Blobs      timingBlobs   `json:"blobs"`
	Ready      []timingReady `json:"ready,omitempty"`

	mu          sync.Mutex
	clock       clock.PassiveClock
	existing    map[string]int64
	transferred sets.Set[string]
}

type timingPhase struct {
	Name       string    `json:"name"`
	Subject    string    `json:"subject,omitempty"`
	Start      time.Time `json:"start"`
	End        time.Time `json:"end"`
	DurationMs float64   `json:"durationMs"`
	Failed     bool      `json:"failed,omitempty"`
}

type timingImage struct {
	App   string `json:"app"`
	Image string `json:"image"`
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
// one-second resolution and the server's clock, so pollLagMs is only that accurate.
type timingReady struct {
	LastTransitionTime time.Time `json:"lastTransitionTime"`
	ObservedAt         time.Time `json:"observedAt"`
	PollLagMs          float64   `json:"pollLagMs"`
}

func (o *progressOutput) timing() *timingTrace {
	if o == nil {
		return nil
	}
	return o.trace
}

func milliseconds(duration time.Duration) float64 {
	return float64(duration.Microseconds()) / 1000
}

func newTimingTrace(clk clock.PassiveClock, command string) *timingTrace {
	return &timingTrace{Command: command, Start: clk.Now(), clock: clk, existing: map[string]int64{}, transferred: sets.New[string]()}
}

// measure starts a phase and returns the function that ends it.
func (t *timingTrace) measure(subject, name string) func() {
	if t == nil {
		return func() {}
	}
	start := t.clock.Now()
	return func() { t.add(timingPhase{Name: name, Subject: subject, Start: start, End: t.clock.Now()}) }
}

// edit starts the "edit" phase of one change in the dev loop, from the watcher's first event, and
// returns the function that ends it when its rollout settles.
func (t *timingTrace) edit(change devChange) func(failed bool) {
	if t == nil {
		return func(bool) {}
	}
	return func(failed bool) {
		t.add(timingPhase{Name: "edit", Subject: change.path, Start: change.firstEvent, End: t.clock.Now(), Failed: failed})
	}
}

func (t *timingTrace) add(phase timingPhase) {
	t.mu.Lock()
	defer t.mu.Unlock()
	phase.DurationMs = milliseconds(phase.End.Sub(phase.Start))
	t.Phases = append(t.Phases, phase)
}

func (t *timingTrace) addImages(apps []protocol.App) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, app := range apps {
		t.Images = append(t.Images, timingImage{App: app.Name, Image: app.Image})
	}
}

// ready records the CLI seeing the Session Ready.
func (t *timingTrace) ready(session protocol.SessionResponse) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	now, transition := t.clock.Now(), time.Time(session.Condition(protocol.ConditionReady).LastTransitionTime)
	t.Ready = append(t.Ready, timingReady{LastTransitionTime: transition, ObservedAt: now, PollLagMs: milliseconds(now.Sub(transition))})
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
				t.add(timingPhase{Name: name, Subject: subject, Start: end.Add(-vertex.Completed.Sub(*vertex.Started)), End: end})
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
	query := request.URL.Query()
	accepted := response.StatusCode >= 200 && response.StatusCode < 300
	switch {
	case request.Method == http.MethodPut && accepted && query.Get("digest") != "":
		t.Blobs.Pushed++
		t.Blobs.BytesPushed += request.ContentLength
		t.transferred.Insert(query.Get("digest"))
		delete(t.existing, query.Get("digest"))
	case request.Method == http.MethodPost && response.StatusCode == http.StatusCreated && query.Get("mount") != "":
		t.Blobs.Mounted++
		t.transferred.Insert(query.Get("mount"))
		delete(t.existing, query.Get("mount"))
	case request.Method == http.MethodPut && accepted && registryManifest.MatchString(request.URL.Path):
		t.Blobs.Manifests++
		t.Blobs.ManifestBytes += request.ContentLength
	case request.Method == http.MethodHead && accepted:
		if match := registryBlob.FindStringSubmatch(request.URL.Path); match != nil && !t.transferred.Has(match[1]) {
			t.existing[match[1]] = response.ContentLength
		}
	}
}

func (t *timingTrace) finish() {
	slices.SortStableFunc(t.Phases, func(a, b timingPhase) int { return a.Start.Compare(b.Start) })
	t.Blobs.Skipped, t.Blobs.BytesSkipped = len(t.existing), 0
	for _, size := range t.existing {
		t.Blobs.BytesSkipped += size
	}
}

// write ends the trace and writes it to path, or to stderr for "-". A nil trace writes nothing.
func (t *timingTrace) write(path string, stderr io.Writer, result error) error {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.End = t.clock.Now()
	t.DurationMs = milliseconds(t.End.Sub(t.Start))
	if result != nil {
		t.Error = result.Error()
	}
	t.finish()
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
