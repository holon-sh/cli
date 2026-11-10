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
		builders: new(builderConnections), development: true,
	}
	defer options.types.close()
	defer options.builders.close()
	resolved, err := resolveManifest(ctx, manager, config.manifest, config.output, apps...)
	if err != nil {
		return err
	}
	if err := options.types.acquire(resolved); err != nil {
		return err
	}
	recordPath := manager.sessionRecordPath(resolved)
	var previous protocol.SessionResponse
	first := options
	if config.devTTLSeconds == nil {
		previous, first.kept = previousSession(ctx, manager, recordPath)
	}
	if previous.Name != "" {
		if err := printStartingEndpoints(stdout, config.output, manager.client.baseURL, previous, resolved.Apps); err != nil {
			return err
		}
	}
	request, err := uploadManifest(ctx, manager, "", resolved, first)
	if err != nil {
		return err
	}
	request.TTLSeconds = config.devTTLSeconds
	config.output.deploying(request.Apps)
	var session protocol.SessionResponse
	if previous.Name != "" {
		session, err = replaceExecution(ctx, manager, previous.Name, previous, request)
	} else if err = manager.Call(ctx, protocol.DevPlane, protocol.ToolCreateSession, request, &session); err == nil {
		err = printStartingEndpoints(stdout, config.output, manager.client.baseURL, session, resolved.Apps)
	} else if status := HTTPStatus(err); status < http.StatusBadRequest || status >= http.StatusInternalServerError {
		console, _ := protocol.PlaneOrigin(manager.client.baseURL, protocol.DevPlane, "")
		err = fmt.Errorf("session creation outcome is unknown; inspect sessions at %s before retrying: %w", console, err)
	}
	if err != nil {
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
		if err := config.output.endpoints(stdout, "Ready", ready, resolved.Apps, false); err != nil {
			return err
		}
		_, err = fmt.Fprintf(stdout, "Expires  %s\n", ready.ExpiresAt)
		return err
	}
	dev := &devLoop{manager: manager, name: session.Name, manifest: resolved, apps: apps, clock: config.clock(), options: options, session: session}
	err = dev.run(ctx, stdout)
	inputs := map[string]string{}
	for name, types := range options.types.accepted {
		inputs[name] = types.inputs
	}
	data, _ := json.Marshal(sessionRecord{Session: session.Name, Inputs: inputs})
	_ = writePrivateFile(recordPath, data)
	return err
}

type sessionRecord struct {
	Session string            `json:"session"`
	Inputs  map[string]string `json:"inputs"`
}

type keptImage struct {
	inputs, image, overlay string
}

func (m *Client) sessionRecordPath(manifest manifestSpec) string {
	key := m.client.baseURL
	for _, app := range manifest.Apps {
		key += "\x00" + app.Name + "\x00" + app.Path
	}
	return filepath.Join(filepath.Dir(m.statePath), digest.FromString(key).Encoded()+".session")
}

func previousSession(ctx context.Context, manager *Client, recordPath string) (protocol.SessionResponse, map[string]keptImage) {
	var record sessionRecord
	var session protocol.SessionResponse
	data, err := readPrivateFile(recordPath)
	if err != nil || json.Unmarshal(data, &record) != nil ||
		manager.Call(ctx, protocol.DevPlane, protocol.ToolRenewSession, protocol.SessionRequest{Session: record.Session}, &session) != nil {
		return protocol.SessionResponse{}, nil
	}
	kept := map[string]keptImage{}
	for _, app := range session.Apps {
		if inputs := record.Inputs[app.Name]; inputs != "" {
			kept[app.Name] = keptImage{inputs: inputs, image: app.Image, overlay: app.Overlay}
		}
	}
	return session, kept
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
	resolved, err := resolveManifest(ctx, manager, manifest, config.output, selection...)
	if err != nil {
		return err
	}
	desired, err := uploadManifest(ctx, manager, protocol.ProductionName, resolved, buildOptions{
		builder: config.builder, HTTPClient: config.RegistryHTTPClient, Stderr: stderr, Env: config.Env, output: config.output,
		builders: builders, architectures: manager.info.BuildArchitectures,
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
	return config.output.endpoints(stdout, "Ready", ready, resolved.Apps, false)
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

func printStartingEndpoints(
	stdout io.Writer, output *progressOutput, origin string, session protocol.SessionResponse, apps []manifestApp,
) error {
	if _, err := fmt.Fprintf(stdout, "Session  %s\n", session.Name); err != nil {
		return err
	}
	starting, err := startingSession(origin, session)
	if err != nil {
		return err
	}
	return output.endpoints(stdout, "Starting", starting, apps, true)
}

func startingSession(origin string, session protocol.SessionResponse) (protocol.SessionResponse, error) {
	base, err := protocol.SessionAudience(origin, protocol.ModeDevelopment, session.Name)
	if err != nil {
		return session, err
	}
	session.Endpoints = make([]protocol.SessionEndpoint, 0, len(session.Apps))
	for _, app := range session.Apps {
		session.Endpoints = append(session.Endpoints, protocol.SessionEndpoint{Name: app.Name, URL: base + protocol.AppPath(app.Name)})
	}
	return session, nil
}

// formatEndpoints lists the apps a command acted on, not the rest of the session. Only a source app can export a
// frontend; a remote, registry or image app has no page the CLI knows of.
func formatEndpoints(session protocol.SessionResponse, apps []manifestApp) (string, error) {
	mode := protocol.ModeDevelopment
	if session.Name == protocol.ProductionName {
		mode = protocol.ModeProduction
	}
	var output []byte
	for _, app := range apps {
		index := slices.IndexFunc(session.Endpoints, func(endpoint protocol.SessionEndpoint) bool { return endpoint.Name == app.Name })
		if index < 0 {
			continue
		}
		endpoint := session.Endpoints[index]
		website, err := url.Parse(endpoint.URL)
		if err != nil {
			return "", err
		}
		if output == nil {
			website.Path = protocol.MCPPath
			output = fmt.Appendf(output, "  MCP tools (all apps)  %s\n", website.String())
		}
		output = fmt.Appendf(output, "\n  %s\n", app.Name)
		if app.Path != "" && !app.resolved.stdio {
			website.Path = strings.TrimSuffix(protocol.AppBrowserMount(app.resolved.App, mode), "/") + "/"
			output = fmt.Appendf(output, "    Browser  %s\n", website.String())
		}
		output = fmt.Appendf(output, "    MCP      %s\n", endpoint.URL)
	}
	if output == nil {
		return "", nil
	}
	return string(append(output, '\n')), nil
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
