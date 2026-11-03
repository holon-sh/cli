package cloud

import (
	"cmp"
	"context"
	"crypto/rand"
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

	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/utils/clock"
	"k8s.io/utils/ptr"

	"github.com/holon-sh/cli/pkg/protocol"
)

const (
	cleanupTimeout   = 10 * time.Second
	workflowPoll     = time.Second
	renewalInterval  = time.Minute
	maxCommandOutput = 64 << 10
)

type commandConfig struct {
	manifest           manifestSpec
	file               string
	RegistryHTTPClient *http.Client
	Auth               ClientConfig
	Env                []string
	Clock              clock.WithTicker
	Input              io.Reader
	verbose            bool
	output             *progressOutput
}

func (c commandConfig) clock() clock.WithTicker {
	return cmp.Or[clock.WithTicker](c.Clock, clock.RealClock{})
}

func resolveManifestFile(arg string) (manifestSpec, error) {
	if arg == "" {
		arg = manifestFileName
		if _, err := os.Lstat(arg); errors.Is(err, os.ErrNotExist) {
			arg = "holon.yml"
		}
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
	return manifest, nil
}

func uploadManifest(
	ctx context.Context, manager *Client, name string, manifest manifestSpec, options buildOptions, apps ...string,
) (protocol.AppsRequestFor[protocol.App], manifestSpec, error) {
	if _, err := manager.controlToken(ctx, ControlPlane(name)); err != nil {
		return protocol.AppsRequestFor[protocol.App]{}, manifestSpec{}, err
	}
	options.output.status("", "Preparing apps", progressWorking)
	roots := slices.Clone(apps)
	for name, app := range manifest.retained {
		if slices.ContainsFunc(app.Requires, func(dependency string) bool { return slices.Contains(apps, dependency) }) {
			roots = append(roots, name)
		}
	}
	resolved, err := resolveManifestGraph(ctx, manifest, roots...)
	if err != nil {
		return protocol.AppsRequestFor[protocol.App]{}, manifestSpec{}, err
	}
	if err := protocol.AccessPolicy(resolved.declarations(), manager.info.OAuth != nil); err != nil {
		return protocol.AppsRequestFor[protocol.App]{}, manifestSpec{}, err
	}
	prepared := resolved
	prepared.Apps = slices.DeleteFunc(slices.Clone(resolved.Apps), func(app manifestApp) bool {
		_, retained := manifest.retained[app.Name]
		return retained
	})
	built, err := buildManifestImages(ctx, manager, name, prepared, options)
	if err != nil {
		return protocol.AppsRequestFor[protocol.App]{}, manifestSpec{}, err
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

type SessionFailure struct{ Detail string }

func (e *SessionFailure) Error() string { return "session failed: " + e.Detail }

func SessionReady(session protocol.SessionResponse) (bool, error) {
	if lifecyclePending(session) {
		if lifecycleFailed(session) {
			return false, &SessionFailure{Detail: session.Lifecycle.Message}
		}
		return false, nil
	}
	condition := session.Condition(protocol.ConditionReady)
	if condition.Status == "True" {
		if len(session.Endpoints) == 0 {
			return false, &SessionFailure{Detail: "ready session returned no endpoint"}
		}
		return true, nil
	}
	if terminalCondition(condition) {
		return false, &SessionFailure{Detail: strings.TrimSuffix(condition.Reason+": "+condition.Message, ": ")}
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
	switch {
	case HTTPStatus(err) == http.StatusNotFound:
	case err != nil:
		return err
	case current.UID == "" || current.Revision == "" || len(current.Apps) == 0:
		return errors.New("control API did not return complete production state")
	}
	manifest.retained = map[string]protocol.App{}
	for _, app := range current.Apps {
		if len(selection) > 0 && !slices.Contains(selection, app.Name) {
			manifest.retained[app.Name] = app
		}
	}
	desired, _, err := uploadManifest(ctx, manager, protocol.ProductionName, manifest, buildOptions{
		HTTPClient: config.RegistryHTTPClient, Stderr: stderr, Env: config.Env, output: config.output,
	}, selection...)
	if err != nil {
		return err
	}
	built := desired.Apps
	if len(selection) > 0 {
		desired.Apps = append(protocol.WritableApps(slices.Collect(maps.Values(manifest.retained))), built...)
	}
	config.output.deploying(built)
	accepted, err := replaceExecution(ctx, manager, protocol.ProductionName, current, desired, len(selection) > 0)
	if err != nil {
		return err
	}
	ready, err := waitSessionReady(ctx, manager, protocol.ProductionName, accepted, config.output)
	if err != nil {
		return err
	}
	config.output.apps(built, "ready", progressDone)
	return printEndpoints(stdout, ready)
}

func waitSessionReady(
	ctx context.Context, manager *Client, name string, accepted protocol.SessionResponse, output *progressOutput,
) (protocol.SessionResponse, error) {
	finish := output.measure(name, "rollout")
	defer finish()
	var result protocol.SessionResponse
	err := wait.PollUntilContextCancel(ctx, workflowPoll, true, func(ctx context.Context) (bool, error) {
		session, err := manager.Session(ctx, name)
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

func (m *Client) StreamLogs(ctx context.Context, input protocol.SessionLogsRequest, stdout io.Writer) error {
	token, err := m.controlToken(ctx, ControlPlane(input.Session))
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

func printEndpoints(stdout io.Writer, session protocol.SessionResponse) error {
	output := []byte("\nReady\n")
	mode := protocol.ModeDevelopment
	if session.Name == protocol.ProductionName {
		mode = protocol.ModeProduction
	}
	var apps []byte
	for i, endpoint := range session.Endpoints {
		website, err := url.Parse(endpoint.URL)
		if err != nil {
			return err
		}
		name, ok := protocol.ParseAppPath(website.Path)
		if !ok || name != endpoint.Name {
			return errors.New("control API returned an invalid MCP endpoint")
		}
		if i == 0 {
			website.Path = protocol.MCPPath
			output = fmt.Appendf(output, "  MCP tools (all apps)  %s\n", website.String())
		}
		website.Path = protocol.WebPath(name)
		for _, app := range session.Apps {
			if app.Name == name {
				website.Path = strings.TrimSuffix(protocol.AppBrowserMount(app, mode), "/") + "/"
			}
		}
		apps = fmt.Appendf(apps, "\n  %s\n    Browser  %s\n    MCP      %s\n", name, website.String(), endpoint.URL)
	}
	output = append(output, apps...)
	_, err := stdout.Write(append(output, '\n'))
	return err
}
