package installation

import (
	"bytes"
	"cmp"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/utils/clock"
	"k8s.io/utils/ptr"

	"github.com/holon-sh/protocol"
	"github.com/opencontainers/go-digest"
)

const (
	workflowPoll     = time.Second
	renewalInterval  = time.Minute
	maxCommandOutput = 64 << 10

	// sessionWait is how long the server holds a get_session that names a revision.
	sessionWait = 30 * time.Second
)

type commandConfig struct {
	manifest           manifestSpec
	file               string
	apps               []string
	devTTLSeconds      *int64
	builder            buildApplication
	RegistryHTTPClient *http.Client
	Auth               ClientConfig
	Clock              clock.WithTickerAndDelayedExecution
	watch              func() (fileWatcher, error)
	verbose            bool
	jsonOutput         bool
	output             *progressOutput
}

func (c commandConfig) buildOptions(manager *Client, stderr io.Writer) buildOptions {
	return buildOptions{
		builder: c.builder, HTTPClient: c.RegistryHTTPClient, Stderr: stderr, Env: c.Auth.Env, output: c.output,
		architectures: manager.info.BuildArchitectures, builders: new(builderConnections),
	}
}

func (c commandConfig) clock() clock.WithTickerAndDelayedExecution {
	return cmp.Or[clock.WithTickerAndDelayedExecution](c.Clock, clock.RealClock{})
}

func resolveManifestFile(arg string) (manifestSpec, error) {
	if arg == "" {
		defaults := []string{manifestFileName, "holon.yml"}
		i := slices.IndexFunc(defaults, func(name string) bool {
			_, err := os.Lstat(name)
			return !errors.Is(err, os.ErrNotExist)
		})
		if i < 0 {
			return manifestSpec{}, nil
		}
		arg = defaults[i]
	}
	absolute, err := filepath.Abs(arg)
	if err != nil {
		return manifestSpec{}, fmt.Errorf("resolve config file: %w", err)
	}
	if err := regularNoSymlink(absolute, "manifest"); err != nil {
		return manifestSpec{}, fmt.Errorf("read manifest %s: %w", absolute, err)
	}
	data, err := os.ReadFile(absolute)
	if err != nil {
		return manifestSpec{}, fmt.Errorf("read manifest %s: %w", absolute, err)
	}
	manifest, err := parseManifest(data, absolute)
	if err != nil {
		return manifestSpec{}, err
	}
	manifest.file = absolute
	for i := range manifest.Apps {
		app := &manifest.Apps[i]
		if app.Path != "" {
			app.Path = filepath.Join(filepath.Dir(absolute), app.Path)
		}
	}
	return manifest, nil
}

func resolveManifest(ctx context.Context, manager *Client, manifest manifestSpec, output *progressOutput, apps ...string) (manifestSpec, error) {
	if _, err := manager.controlToken(ctx); err != nil {
		return manifestSpec{}, err
	}
	output.status("", "Preparing apps", progressWorking)
	resolved, err := resolveManifestGraph(ctx, manifest, apps...)
	if err != nil {
		return manifestSpec{}, err
	}
	return resolved, protocol.AccessPolicy(resolved.declarations(), manager.info.OAuth != nil)
}

func uploadManifest(
	ctx context.Context, manager *Client, name string, resolved manifestSpec, options buildOptions,
) (protocol.AppsRequestFor[protocol.App], error) {
	prepared, cleanup, err := prepareManifestTypes(ctx, resolved, options)
	if err != nil {
		return protocol.AppsRequestFor[protocol.App]{}, err
	}
	defer cleanup()
	if options.served != nil {
		options.types.accept(prepared)
	}
	built, err := buildManifestImages(ctx, manager, name, prepared, options)
	if err != nil {
		return protocol.AppsRequestFor[protocol.App]{}, err
	}
	if err := currentTypeSources(prepared); err != nil {
		return protocol.AppsRequestFor[protocol.App]{}, err
	}
	return protocol.AppsRequestFor[protocol.App]{RequestID: strings.ToLower(rand.Text()), Apps: built}, nil
}

func imageDestination(ctx context.Context, manager *Client, name string, manifest manifestSpec) (protocol.UploadGrant, error) {
	names := make([]string, 0, len(manifest.Apps))
	for _, app := range manifest.Apps {
		names = append(names, app.Name)
	}
	if prefix := manager.info.ImageRepositoryPrefix; prefix != "" {
		host, repository, _ := strings.Cut(prefix, "/")
		repositories := make(map[string]string, len(names))
		for _, name := range names {
			repositories[name] = repository + "/" + name
		}
		return protocol.UploadGrant{Registry: host, Repositories: repositories}, nil
	}
	var grant protocol.UploadGrant
	upload := protocol.UploadRequest{Apps: names}
	err := manager.Call(ctx, ControlPlane(name), protocol.ToolUploadGrant, upload, &grant)
	return grant, err
}

func runDev(ctx context.Context, apps []string, stdout, stderr io.Writer, config commandConfig) error {
	resultWriter := stdout
	if config.jsonOutput {
		stdout = stderr
	}
	config.output.status("", "Authenticating", progressWorking)
	manager, err := NewClient(ctx, config.Auth)
	if err != nil {
		return err
	}
	options := config.buildOptions(manager, stderr)
	options.types, options.development = new(typeWorkspace), true
	defer options.types.close()
	defer options.builders.close()
	resolved, err := resolveManifest(ctx, manager, config.manifest, config.output, apps...)
	if err != nil {
		return err
	}
	if err := options.types.acquire(resolved); err != nil {
		return err
	}
	recordPath := manager.sessionRecordPath(config.manifest, apps)
	dev := &devLoop{
		manager: manager, manifest: resolved, apps: apps, clock: config.clock(), watch: config.watch, started: config.clock().Now(), options: options,
		pending: sets.New[string](), announce: true,
		sent: map[string]string{}, unbuilt: sets.New[string](), background: context.WithoutCancel(ctx), recording: map[string]context.CancelCauseFunc{},
	}
	defer dev.records.Wait()
	first := options
	if config.devTTLSeconds == nil {
		dev.session = previousSession(ctx, manager, recordPath)
		dev.name = dev.session.Name
		first.overlays = recordedSources(dev.session.Apps)
		if first.served, err = dev.served(ctx); err == nil {
			dev.sent = maps.Clone(first.overlays)
		}
	}
	if dev.name != "" {
		if err := printStartingEndpoints(stdout, config.output, manager, dev.name, resolved.Apps); err != nil {
			return err
		}
	}
	request, err := uploadManifest(ctx, manager, "", resolved, first)
	if err != nil {
		return err
	}
	request.TTLSeconds = config.devTTLSeconds
	config.output.deploying(request.Apps)
	if dev.name != "" {
		err = dev.update(ctx, request, removedApps(dev.session.Apps, request.Apps)...)
	} else if err = manager.Call(ctx, protocol.DevPlane, protocol.ToolCreateSession, request, &dev.session); err == nil {
		dev.name = dev.session.Name
		for _, app := range request.Apps {
			dev.pending.Insert(app.Name)
		}
		err = printStartingEndpoints(stdout, config.output, manager, dev.name, resolved.Apps)
	} else if status := HTTPStatus(err); status < http.StatusBadRequest || status >= http.StatusInternalServerError {
		console, _ := protocol.PlaneOrigin(manager.client.baseURL, protocol.DevPlane, "")
		err = fmt.Errorf("session creation outcome is unknown; inspect sessions at %s before retrying: %w", console, err)
	}
	if err != nil {
		return err
	}
	if request.TTLSeconds != nil {
		ready, err := waitSessionReady(ctx, manager, dev.name, dev.session, config.output)
		if err == nil {
			dev.session = ready
			err = dev.generate(ctx)
		}
		if err != nil {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Minute)
			defer cancel()
			return errors.Join(err, manager.DeleteSession(cleanup, dev.name, dev.session.UID, true))
		}
		if config.jsonOutput {
			return json.NewEncoder(resultWriter).Encode(dev.session)
		}
		if err := config.output.endpoints(stdout, "Ready", manager, dev.name, resolved.Apps, false); err != nil {
			return err
		}
		_, err = fmt.Fprintf(stdout, "Expires  %s\n", dev.session.ExpiresAt)
		return err
	}
	err = dev.run(ctx, stdout)
	data, _ := json.Marshal(sessionRecord{Session: dev.name})
	_ = writePrivateFile(recordPath, data)
	return err
}

type sessionRecord struct {
	Session string `json:"session"`
}

func (m *Client) sessionRecordPath(manifest manifestSpec, selection []string) string {
	key := m.client.baseURL + "\x00" + manifest.file
	for _, app := range manifest.sources {
		key += "\x00" + app.Name + "\x00" + app.Path
	}
	key += "\x00" + strings.Join(selection, "\x00")
	return filepath.Join(filepath.Dir(m.statePath), digest.FromString(key).Encoded()+".dev-session")
}

func previousSession(ctx context.Context, manager *Client, recordPath string) protocol.SessionResponse {
	var record sessionRecord
	var session protocol.SessionResponse
	data, err := readPrivateFile(recordPath)
	if err != nil || json.Unmarshal(data, &record) != nil ||
		manager.Call(ctx, protocol.DevPlane, protocol.ToolRenewSession, protocol.SessionRequest{Session: record.Session}, &session) != nil {
		return protocol.SessionResponse{}
	}
	return session
}

// sessionAudience is the origin that serves session name, and the mode the session runs in.
func (m *Client) sessionAudience(name string) (audience, mode string, err error) {
	mode = protocol.ModeDevelopment
	if name == protocol.ProductionName {
		mode = protocol.ModeProduction
	}
	audience, err = protocol.SessionAudience(m.client.baseURL, mode, name)
	return audience, mode, err
}

func ControlPlane(name string) protocol.Plane {
	if name == protocol.ProductionName {
		return protocol.AdminPlane
	}
	return protocol.DevPlane
}

func (m *Client) Session(ctx context.Context, name string) (protocol.SessionResponse, error) {
	var session protocol.SessionResponse
	err := m.Call(ctx, ControlPlane(name), protocol.ToolGetSession, protocol.SessionRequest{Session: name}, &session)
	return session, err
}

type sessionWaitRequest struct {
	protocol.SessionRequest

	Revision string `json:"revision,omitempty"`
}

// awaitSession reads a session once it has moved past revision, the last one the caller saw, which the
// server waits for up to sessionWait. Without a revision it answers at once.
func (m *Client) awaitSession(ctx context.Context, name, revision string) (protocol.SessionResponse, error) {
	var session protocol.SessionResponse
	request := sessionWaitRequest{SessionRequest: protocol.SessionRequest{Session: name}, Revision: revision}
	return session, m.Call(ctx, ControlPlane(name), protocol.ToolGetSession, request, &session)
}

func SessionReady(session protocol.SessionResponse) (bool, error) {
	if lifecyclePending(session) {
		if lifecycleFailed(session) {
			return false, errors.New("session failed: " + session.Lifecycle.Message)
		}
		return false, nil
	}
	condition := session.Condition(protocol.ConditionReady)
	if condition.Status == "True" {
		return true, nil
	}
	if terminalCondition(condition) {
		return false, errors.New("session failed: " + strings.TrimSuffix(condition.Reason+": "+condition.Message, ": "))
	}
	return false, nil
}

func terminalCondition(condition protocol.Condition) bool {
	return condition.Status == "False" && slices.Contains([]string{
		protocol.ReasonInvalidIdentity, protocol.ReasonInvalidExpiry, protocol.ReasonInvalidSpec,
		protocol.ReasonLeaseExpired, protocol.ReasonDeleting, protocol.ReasonProgressDeadline,
	}, condition.Reason)
}

func runDeploy(ctx context.Context, selection []string, stdout, stderr io.Writer, config commandConfig) error {
	finish := config.output.measure("production", "deploy")
	defer finish()
	config.output.status("", "Authenticating", progressWorking)
	manifest := config.manifest
	manager, err := NewClient(ctx, config.Auth)
	if err != nil {
		return err
	}
	current, err := manager.Session(ctx, protocol.ProductionName)
	if err != nil && HTTPStatus(err) != http.StatusNotFound {
		return err
	}
	manifest.deployed = map[string]*protocol.AppAccess{}
	for _, app := range current.Apps {
		manifest.deployed[app.Name] = app.Access
	}
	options := config.buildOptions(manager, stderr)
	defer options.builders.close()
	resolved, err := resolveManifest(ctx, manager, manifest, config.output, selection...)
	if err != nil {
		return err
	}
	desired, err := uploadManifest(ctx, manager, protocol.ProductionName, resolved, options)
	if err != nil {
		return err
	}
	config.output.deploying(desired.Apps)
	accepted, err := replaceExecution(ctx, manager, protocol.ProductionName, current, desired)
	if err != nil {
		return err
	}
	ready, err := waitSessionReady(ctx, manager, protocol.ProductionName, accepted, config.output)
	if err != nil {
		return err
	}
	config.output.apps(desired.Apps, "ready", progressDone)
	return config.output.endpoints(stdout, "Ready", manager, ready.Name, resolved.Apps, false)
}

func waitSessionReady(
	ctx context.Context, manager *Client, name string, accepted protocol.SessionResponse, output *progressOutput,
) (protocol.SessionResponse, error) {
	finish := output.measure(name, "rollout")
	defer finish()
	result := accepted
	starting := startingApps(accepted)
	err := wait.PollUntilContextCancel(ctx, workflowPoll, true, func(ctx context.Context) (bool, error) {
		session, err := manager.awaitSession(ctx, name, result.Revision)
		if err != nil {
			return false, err
		}
		result = session
		if session.UID != accepted.UID || len(protocol.ChangedApps(session.Apps, accepted.Apps)) != 0 || session.Generation < accepted.Generation {
			return false, errors.New("deployment was superseded")
		}
		output.serving(session, starting)
		reportSessionProgress(session, output, "Deploying apps", progressWorking)
		if err := manager.authorizeOperation(ctx, session); err != nil {
			return false, err
		}
		return SessionReady(session)
	})
	return result, err
}

func (o *progressOutput) deploying(apps []protocol.App) {
	o.status("", "Deploying apps", progressWorking)
	o.apps(apps, "deploying", progressWorking)
}

func (o *progressOutput) apps(apps []protocol.App, phase string, state progressState) {
	for _, app := range apps {
		o.status(app.Name, phase, state)
	}
}

func startingApps(accepted protocol.SessionResponse) sets.Set[string] {
	starting := sets.New[string]()
	for _, app := range accepted.Apps {
		starting.Insert(app.Name)
	}
	for _, endpoint := range accepted.Endpoints {
		starting.Delete(endpoint.Name)
	}
	return starting
}

func (o *progressOutput) serving(session protocol.SessionResponse, starting sets.Set[string]) {
	if lifecyclePending(session) {
		return
	}
	for _, endpoint := range session.Endpoints {
		if starting.Has(endpoint.Name) {
			o.status(endpoint.Name, "ready", progressDone)
		}
	}
}

func reportSessionProgress(
	session protocol.SessionResponse, output *progressOutput, phase string, state progressState,
) {
	name := ""
	condition := session.Condition(protocol.ConditionReady)
	if lifecyclePending(session) {
		lifecycle := ptr.Deref(session.Lifecycle, protocol.LifecycleStatus{Phase: "Preparing lifecycle"})
		name, phase, state = lifecycle.App, lifecycle.Phase, progressWorking
		if lifecycle.Hook != "" {
			phase = lifecycle.Hook + ": " + phase
		}
		if lifecycle.Phase == "Failed" {
			phase, state = phase+": "+lifecycle.Message, progressError
		}
	} else if condition.Reason == "WaitingForConfiguration" {
		phase, state = condition.Message, progressBlocked
	}
	output.status(name, phase, state)
}

func printStartingEndpoints(stdout io.Writer, output *progressOutput, manager *Client, name string, apps []manifestApp) error {
	if _, err := fmt.Fprintf(stdout, "Session  %s\n", name); err != nil {
		return err
	}
	return output.endpoints(stdout, "Starting", manager, name, apps, true)
}

// summary lists the endpoints of apps, the ones a command acted on, not the rest of session name. Only a source app can
// export a frontend; a remote, registry or image app has no page the CLI knows of.
func (m *Client) summary(name string, apps []manifestApp) (string, error) {
	audience, mode, err := m.sessionAudience(name)
	if err != nil || len(apps) == 0 {
		return "", err
	}
	output := fmt.Appendf(nil, "  MCP tools (all apps)  %s\n", audience+protocol.MCPPath)
	for _, app := range apps {
		output = fmt.Appendf(output, "\n  %s\n", app.Name)
		if app.Path != "" && !app.resolved.stdio {
			output = fmt.Appendf(output, "    Browser  %s%s/\n", audience, strings.TrimSuffix(protocol.AppBrowserMount(app.resolved.App, mode), "/"))
		}
		output = fmt.Appendf(output, "    MCP      %s\n", audience+protocol.AppPath(app.Name))
	}
	return string(append(output, '\n')), nil
}

// StreamLogs follows session logs until ctx ends or the server rejects the request. A dropped stream resumes from
// the last record received, skipping the records the server replays.
func (m *Client) StreamLogs(ctx context.Context, input protocol.SessionLogsRequest, stdout io.Writer) error {
	return m.streamLogs(ctx, input, &resumingWriter{out: stdout})
}

func (m *Client) streamLogs(ctx context.Context, input protocol.SessionLogsRequest, records *resumingWriter) error {
	query := url.Values{"follow": {"true"}}
	for key, value := range map[string]string{
		"app": input.App, "since": input.Since, "traceId": input.TraceID, "text": input.Text,
		"operation": input.Operation, "hook": input.Hook, "event": input.Event, "tool": input.Tool, "status": input.Status,
	} {
		if value != "" {
			query.Set(key, value)
		}
	}
	if input.Attempt != nil {
		query.Set("attempt", strconv.FormatInt(*input.Attempt, 10))
	}
	for delay := time.Second; ; delay = min(2*delay, 30*time.Second) {
		received := records.last
		token, err := m.controlToken(ctx)
		if err == nil {
			logPath := (&url.URL{Path: path.Join(protocol.APIPathSessions, input.Session, "logs"), RawQuery: query.Encode()}).String()
			err = m.client.send(ctx, logPath, apiRequest{Plane: ControlPlane(input.Session), Token: token, Stream: records}, nil)
		} else if !errors.Is(err, errRefreshFailed) {
			return err
		}
		if records.err != nil || !resumable(err) {
			return err
		}
		if !records.last.Equal(received) {
			delay = time.Second
			query.Set("since", records.last.Format(time.RFC3339Nano))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
		records.partial = records.partial[:0]
	}
}

// resumingWriter writes whole log lines, tracking the timestamp that leads each record. Records not after the last
// one written, with their continuation lines, are replays and are dropped.
type resumingWriter struct {
	out     io.Writer
	stdio   bool
	last    time.Time
	drop    bool
	partial []byte
	err     error
}

func logTime(at time.Time) string {
	return at.Local().Format(time.TimeOnly)
}

func (w *resumingWriter) Write(p []byte) (int, error) {
	w.partial = append(w.partial, p...)
	end := bytes.LastIndexByte(w.partial, '\n') + 1
	var kept []byte
	for line := range bytes.Lines(w.partial[:end]) {
		token, rest, _ := bytes.Cut(bytes.TrimSpace(line), []byte(" "))
		if at, err := time.Parse(time.RFC3339Nano, string(token)); err == nil {
			w.drop = !at.After(w.last) || w.stdio && !bytes.HasPrefix(rest, []byte(" "))
			if at.After(w.last) {
				w.last = at
			}
			line = bytes.Replace(line, token, []byte(logTime(at)), 1)
		}
		if !w.drop {
			kept = append(kept, line...)
		}
	}
	w.partial = append(w.partial[:0], w.partial[end:]...)
	if len(kept) > 0 {
		if _, w.err = w.out.Write(kept); w.err != nil {
			return 0, w.err
		}
	}
	return len(p), nil
}
