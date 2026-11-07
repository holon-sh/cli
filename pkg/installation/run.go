package installation

import (
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
)

const (
	workflowPoll     = time.Second
	idlePoll         = 5 * time.Second
	renewalInterval  = time.Minute
	maxCommandOutput = 64 << 10

	// sessionWaitSeconds is how long a get_session long poll may wait on the server.
	sessionWaitSeconds = 30
)

type commandConfig struct {
	manifest           manifestSpec
	file               string
	apps               []string
	devTTLSeconds      *int64
	check              bool
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

func uploadManifest(
	ctx context.Context, manager *Client, name string, manifest manifestSpec, options buildOptions, apps ...string,
) (protocol.AppsRequestFor[protocol.App], manifestSpec, error) {
	if _, err := manager.controlToken(ctx); err != nil {
		return protocol.AppsRequestFor[protocol.App]{}, manifestSpec{}, err
	}
	options.output.status("", "Preparing apps", progressWorking)
	resolved, err := resolveManifestGraph(ctx, manifest, apps...)
	if err != nil {
		return protocol.AppsRequestFor[protocol.App]{}, manifestSpec{}, err
	}
	if err := protocol.AccessPolicy(resolved.declarations(), manager.info.OAuth != nil); err != nil {
		return protocol.AppsRequestFor[protocol.App]{}, manifestSpec{}, err
	}
	if options.types != nil {
		if err := options.types.acquire(resolved); err != nil {
			return protocol.AppsRequestFor[protocol.App]{}, manifestSpec{}, err
		}
	}
	prepared, cleanup, err := prepareManifestTypes(ctx, resolved, options)
	if err != nil {
		return protocol.AppsRequestFor[protocol.App]{}, manifestSpec{}, err
	}
	defer cleanup()
	built, err := buildManifestImages(ctx, manager, name, prepared, options)
	if err != nil {
		return protocol.AppsRequestFor[protocol.App]{}, manifestSpec{}, err
	}
	if err := currentTypeSources(prepared); err != nil {
		return protocol.AppsRequestFor[protocol.App]{}, manifestSpec{}, err
	}
	if options.types != nil {
		options.types.accept(prepared)
	}
	return protocol.AppsRequestFor[protocol.App]{RequestID: strings.ToLower(rand.Text()), Apps: built}, resolved, nil
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
		builders: new(builderConnections), check: config.check,
	}
	defer options.types.close()
	defer options.builders.close()
	request, resolvedManifest, err := uploadManifest(ctx, manager, "", config.manifest, options, apps...)
	if err != nil {
		return err
	}
	request.TTLSeconds = config.devTTLSeconds
	token, err := manager.controlToken(ctx)
	if err != nil {
		return err
	}
	config.output.deploying(request.Apps)
	var session protocol.SessionResponse
	if err := manager.client.Call(ctx, protocol.DevPlane, token, protocol.ToolCreateSession, request, &session); err != nil {
		if status := HTTPStatus(err); status >= http.StatusBadRequest && status < http.StatusInternalServerError {
			return err
		}
		console, _ := protocol.PlaneOrigin(manager.client.baseURL, protocol.DevPlane, "")
		return fmt.Errorf("session creation outcome is unknown; inspect sessions at %s before retrying: %w", console, err)
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
		if err := printEndpoints(stdout, ready); err != nil {
			return err
		}
		_, err = fmt.Fprintf(stdout, "Expires  %s\n", ready.ExpiresAt)
		return err
	}
	dev := &devLoop{manager: manager, name: session.Name, manifest: resolvedManifest, apps: apps, clock: config.clock(), options: options, session: session}
	return errors.Join(dev.run(ctx, stdout), deleteWithFreshContext(manager, session.Name, session.UID))
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

	WaitSeconds int    `json:"waitSeconds"`
	Revision    string `json:"revision,omitempty"`
}

// awaitSession reads a session that is still converging, letting the server hold the answer until it
// moves past revision, the last one the caller saw, so that a change between two reads is not missed.
// An installation that refuses the wait, which is any failure that a plain read does not
// share, is read plainly from then on.
func (m *Client) awaitSession(ctx context.Context, name, revision string) (protocol.SessionResponse, error) {
	if m.plainSessionReads.Load() {
		return m.Session(ctx, name)
	}
	var session protocol.SessionResponse
	request := sessionWaitRequest{SessionRequest: protocol.SessionRequest{Session: name}, WaitSeconds: sessionWaitSeconds, Revision: revision}
	err := m.Call(ctx, ControlPlane(name), protocol.ToolGetSession, request, &session)
	if err == nil {
		return session, nil
	}
	session, err = m.Session(ctx, name)
	m.plainSessionReads.Store(err == nil)
	return session, err
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
	desired, _, err := uploadManifest(ctx, manager, protocol.ProductionName, manifest, buildOptions{
		builder: config.builder, HTTPClient: config.RegistryHTTPClient, Stderr: stderr, Env: config.Env,
		output: config.output, builders: builders, architectures: manager.info.BuildArchitectures, check: true,
	}, selection...)
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
	return printEndpoints(stdout, ready)
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
	base, err := protocol.PlaneOrigin(origin, protocol.SessionPlane, session.Name)
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
	return printEndpointsAs(stdout, "Starting", session)
}

func printEndpoints(stdout io.Writer, session protocol.SessionResponse) error {
	return printEndpointsAs(stdout, "Ready", session)
}

func printEndpointsAs(stdout io.Writer, heading string, session protocol.SessionResponse) error {
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

func (m *Client) StreamLogs(ctx context.Context, input protocol.SessionLogsRequest, stdout io.Writer) error {
	token, err := m.controlToken(ctx)
	if err != nil {
		return err
	}
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
	logPath := (&url.URL{Path: sessionPath(input.Session, "logs"), RawQuery: query.Encode()}).String()
	return m.client.Do(ctx, http.MethodGet, logPath, apiRequest{Plane: ControlPlane(input.Session), Token: token, Stream: stdout}, nil)
}
