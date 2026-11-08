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
	Env                []string
	Clock              clock.WithTicker
	verbose            bool
	jsonOutput         bool
	output             *progressOutput
}

func (c commandConfig) clock() clock.WithTicker {
	return cmp.Or[clock.WithTicker](c.Clock, clock.RealClock{})
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

// resolveManifest resolves the apps a command runs, with their dependencies, and checks their access rules.
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
	if options.types != nil {
		if err := options.types.acquire(resolved); err != nil {
			return protocol.AppsRequestFor[protocol.App]{}, err
		}
	}
	prepared, cleanup, err := prepareManifestTypes(ctx, resolved, options)
	if err != nil {
		return protocol.AppsRequestFor[protocol.App]{}, err
	}
	defer cleanup()
	built, err := buildManifestImages(ctx, manager, name, prepared, options)
	if err != nil {
		return protocol.AppsRequestFor[protocol.App]{}, err
	}
	if err := currentTypeSources(prepared); err != nil {
		return protocol.AppsRequestFor[protocol.App]{}, err
	}
	if options.types != nil {
		options.types.accept(prepared)
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
	options := buildOptions{
		types:   new(typeWorkspace),
		builder: config.builder, HTTPClient: config.RegistryHTTPClient,
		Stderr: stderr, Env: config.Env, architectures: manager.info.BuildArchitectures, output: config.output,
		builders: new(builderConnections),
	}
	defer options.types.close()
	defer options.builders.close()
	resolved, err := resolveManifest(ctx, manager, config.manifest, config.output, apps...)
	if err != nil {
		return err
	}
	var previous protocol.SessionResponse
	if config.devTTLSeconds == nil {
		previous = previousSession(ctx, manager, resolved)
	}
	// Only the start reuses the previous session's images, for apps whose source the reload below brings up to date.
	start := options
	start.reusable, start.reuse = sessionImages(previous), inputsAnnotation
	request, err := uploadManifest(ctx, manager, "", resolved, start)
	if err != nil {
		return err
	}
	request.TTLSeconds = config.devTTLSeconds
	config.output.deploying(request.Apps)
	session := previous
	switch {
	case previous.Name == "":
		token, err := manager.controlToken(ctx)
		if err != nil {
			return err
		}
		if err := manager.client.Call(ctx, protocol.DevPlane, token, protocol.ToolCreateSession, request, &session); err != nil {
			if status := HTTPStatus(err); status >= http.StatusBadRequest && status < http.StatusInternalServerError {
				return err
			}
			console, _ := protocol.PlaneOrigin(manager.client.baseURL, protocol.DevPlane, "")
			return fmt.Errorf("session creation outcome is unknown; inspect sessions at %s before retrying: %w", console, err)
		}
	case len(protocol.ChangedApps(previous.Apps, request.Apps)) != 0:
		if session, err = replaceExecution(ctx, manager, previous.Name, previous, request); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(stdout, "Session  %s\n", session.Name); err != nil {
		return err
	}
	if err := printStartingEndpoints(stdout, manager.client.baseURL, session); err != nil {
		return err
	}
	if request.TTLSeconds != nil {
		ready, err := waitSessionReady(ctx, manager, session.Name, session, config.output)
		if err != nil {
			return errors.Join(err, deleteWithFreshContext(manager, session.Name, session.UID))
		}
		if config.jsonOutput {
			return json.NewEncoder(resultWriter).Encode(ready)
		}
		if err := printEndpoints(stdout, "Ready", ready); err != nil {
			return err
		}
		_, err = fmt.Fprintf(stdout, "Expires  %s\n", ready.ExpiresAt)
		return err
	}
	dev := &devLoop{
		manager: manager, name: session.Name, manifest: resolved, apps: apps, clock: config.clock(), options: options, session: session,
		stale: reusedSources(resolved, previous, request.Apps),
	}
	return dev.run(ctx, stdout)
}

// previousSession renews and returns the development session an earlier holon dev of exactly manifest's apps
// left running, or none. Renewal fails unless the session is the caller's, foreground and unexpired.
func previousSession(ctx context.Context, manager *Client, manifest manifestSpec) protocol.SessionResponse {
	// Without the session list, holon dev starts a new session, as it would with none to pick up.
	sessions, _ := scopeSessions(ctx, manager, "")
	for _, candidate := range sessions {
		var session protocol.SessionResponse
		if slices.EqualFunc(protocol.WritableApps(candidate.Apps), manifest.Apps, func(running protocol.App, app manifestApp) bool {
			return running.Name == app.Name
		}) && manager.Call(ctx, protocol.DevPlane, protocol.ToolRenewSession, protocol.SessionRequest{Session: candidate.Name}, &session) == nil {
			return session
		}
	}
	return protocol.SessionResponse{}
}

// reusedSources are the build contexts of the apps that kept the previous session's image, whose current
// source the session does not run yet.
func reusedSources(manifest manifestSpec, previous protocol.SessionResponse, apps []protocol.App) sets.Set[string] {
	stale := sets.New[string]()
	for _, app := range manifest.Apps {
		current := apps[slices.IndexFunc(apps, func(current protocol.App) bool { return current.Name == app.Name })]
		if app.Path != "" && slices.ContainsFunc(previous.Apps, func(old protocol.App) bool { return old.Name == app.Name && old.Image == current.Image }) {
			stale.Insert(app.Path)
		}
	}
	return stale
}

// sessionImages are the images that sessions run, by app, which a build reuses when they match its source.
func sessionImages(sessions ...protocol.SessionResponse) map[string][]string {
	images := map[string][]string{}
	for _, session := range sessions {
		for _, app := range session.Apps {
			images[app.Name] = append(images[app.Name], app.Image)
		}
	}
	return images
}

func sessionPath(name, suffix string) string {
	return path.Join(protocol.APIPathSessions, name, suffix)
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
		if len(session.Endpoints) == 0 {
			return false, errors.New("session failed: ready session returned no endpoint")
		}
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

func deleteWithFreshContext(manager *Client, name, uid string) error {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	return manager.DeleteSession(cleanupCtx, name, uid, true)
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
	builders := new(builderConnections)
	defer builders.close()
	// Without the session list, deploy builds every image, as it would with no development session.
	sessions, _ := scopeSessions(ctx, manager, "")
	resolved, err := resolveManifest(ctx, manager, manifest, config.output, selection...)
	if err != nil {
		return err
	}
	desired, err := uploadManifest(ctx, manager, protocol.ProductionName, resolved, buildOptions{
		builder: config.builder, HTTPClient: config.RegistryHTTPClient, Stderr: stderr, Env: config.Env, output: config.output,
		builders: builders, architectures: manager.info.BuildArchitectures, reusable: sessionImages(sessions...), reuse: promotionAnnotation,
	})
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
	return printEndpoints(stdout, "Ready", ready)
}

func waitSessionReady(
	ctx context.Context, manager *Client, name string, accepted protocol.SessionResponse, output *progressOutput,
) (protocol.SessionResponse, error) {
	finish := output.measure(name, "rollout")
	defer finish()
	result := accepted
	err := wait.PollUntilContextCancel(ctx, workflowPoll, true, func(ctx context.Context) (bool, error) {
		session, err := manager.awaitSession(ctx, name, result.Revision)
		if err != nil {
			return false, err
		}
		result = session
		if session.UID != accepted.UID || len(protocol.ChangedApps(session.Apps, accepted.Apps)) != 0 || session.Generation < accepted.Generation {
			return false, errors.New("deployment was superseded")
		}
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

// printStartingEndpoints prints where the session's apps will be. The API publishes endpoints only once
// their apps are up, but the URLs follow from the session's name.
func printStartingEndpoints(stdout io.Writer, origin string, session protocol.SessionResponse) error {
	base, err := protocol.SessionAudience(origin, protocol.ModeDevelopment, session.Name)
	if err != nil {
		return err
	}
	session.Endpoints = make([]protocol.SessionEndpoint, 0, len(session.Apps))
	for _, app := range session.Apps {
		session.Endpoints = append(session.Endpoints, protocol.SessionEndpoint{Name: app.Name, URL: base + protocol.AppPath(app.Name)})
	}
	if len(session.Endpoints) == 0 {
		return nil
	}
	return printEndpoints(stdout, "Starting", session)
}

func printEndpoints(stdout io.Writer, heading string, session protocol.SessionResponse) error {
	output := []byte("\n" + heading + "\n")
	mode := protocol.ModeDevelopment
	if session.Name == protocol.ProductionName {
		mode = protocol.ModeProduction
	}
	for i, endpoint := range session.Endpoints {
		website, err := url.Parse(endpoint.URL)
		if err != nil {
			return err
		}
		if i == 0 {
			website.Path = protocol.MCPPath
			output = fmt.Appendf(output, "  MCP tools (all apps)  %s\n", website.String())
		}
		app := protocol.App{Name: endpoint.Name}
		if index := slices.IndexFunc(session.Apps, func(candidate protocol.App) bool { return candidate.Name == endpoint.Name }); index >= 0 {
			app = session.Apps[index]
		}
		website.Path = strings.TrimSuffix(protocol.AppBrowserMount(app, mode), "/") + "/"
		output = fmt.Appendf(output, "\n  %s\n    Browser  %s\n    MCP      %s\n", endpoint.Name, website.String(), endpoint.URL)
	}
	_, err := stdout.Write(append(output, '\n'))
	return err
}

// StreamLogs follows session logs until ctx ends or the server rejects the request. A dropped stream resumes from
// the last record received, skipping the records the server replays.
func (m *Client) StreamLogs(ctx context.Context, input protocol.SessionLogsRequest, stdout io.Writer) error {
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
	records := &resumingWriter{out: stdout}
	for delay := time.Second; ; delay = min(2*delay, 30*time.Second) {
		received := records.last
		// A token refresh that failed in transit is retried like a dropped stream; lost credentials stay final.
		token, err := m.controlToken(ctx)
		if err == nil {
			logPath := (&url.URL{Path: sessionPath(input.Session, "logs"), RawQuery: query.Encode()}).String()
			err = m.client.Do(ctx, http.MethodGet, logPath, apiRequest{Plane: ControlPlane(input.Session), Token: token, Stream: records}, nil)
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
	last    time.Time
	replay  bool
	partial []byte
	err     error
}

func (w *resumingWriter) Write(p []byte) (int, error) {
	w.partial = append(w.partial, p...)
	end := bytes.LastIndexByte(w.partial, '\n') + 1
	for line := range bytes.Lines(w.partial[:end]) {
		token, _, _ := bytes.Cut(bytes.TrimSpace(line), []byte(" "))
		if at, err := time.Parse(time.RFC3339Nano, string(token)); err == nil {
			if w.replay = !at.After(w.last); !w.replay {
				w.last = at
			}
		}
		if !w.replay {
			if _, w.err = w.out.Write(line); w.err != nil {
				return 0, w.err
			}
		}
	}
	w.partial = append(w.partial[:0], w.partial[end:]...)
	return len(p), nil
}
