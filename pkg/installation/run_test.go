package installation

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	sharedcli "github.com/holon-sh/cli/pkg/cli"
	"github.com/holon-sh/cli/pkg/mcpregistry"
	"golang.org/x/oauth2"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	testingclock "k8s.io/utils/clock/testing"

	"github.com/holon-sh/cli/pkg/installation/installationtest"
	"github.com/holon-sh/protocol"
)

func executeCommand(ctx context.Context, args []string, stdout, stderr io.Writer, config commandConfig) error {
	return executeCommandWithInput(ctx, args, nil, stdout, stderr, config)
}

func executeCommandWithInput(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, config commandConfig) error {
	config.Auth.Env = append([]string{"HOLON_ORIGIN="}, config.Auth.Env...)
	command := sharedcli.NewCommand("dev")
	command.AddCommand(newCommands(config)...)
	command.SetArgs(args)
	command.SetIn(stdin)
	command.SetOut(cmp.Or(stdout, io.Discard))
	command.SetErr(cmp.Or(stderr, io.Discard))
	return command.ExecuteContext(ctx)
}

func TestRunRejectsInvalidInputs(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	project := filepath.Join(root, manifestFileName)
	sourceFiles(t, root, map[string]string{manifestFileName: manifestFixture})
	for _, tc := range []struct {
		want string
		args []string
	}{
		{`unknown app "missing"`, []string{"dev", "alpha", "missing", "--project", project}},
		{"--ttl requires --detach", []string{"dev", "--ttl", "24h", "--project", project}},
		{"--json requires --detach", []string{"dev", "--json", "--project", project}},
		{"whole seconds", []string{"dev", "--detach", "--ttl", "0s", "--project", project}},
		{"whole seconds", []string{"dev", "--detach", "--ttl", "1.5s", "--project", project}},
		{"whole seconds", []string{"dev", "--detach", "--ttl", "25h", "--project", project}},
		{"--env must be dev or prod", []string{"config", "set", "--env", "staging", "API_URL", "value"}},
		{"--session requires a development session", []string{"config", "set", "--session", "production", "API_URL", "value"}},
		{`invalid variable name "not-valid"`, []string{"config", "set", "not-valid", "value"}},
	} {
		config := commandConfig{Auth: ClientConfig{ConfigDir: t.TempDir()}}
		if err := executeCommand(t.Context(), tc.args, nil, nil, config); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("invalid command %v: error=%v, want %q", tc.args, err, tc.want)
		}
	}
}

func TestRunPreparationFailures(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ command, access, want, diagnostics, edit string }{
		{command: "deploy", access: "--public", want: "fixture build failed", diagnostics: "missing dependency: fixture-package"},
		{command: "dev", access: "--oauth", want: "requires a configured identity provider"},
		{command: "deploy", access: "--public", want: "source changed during preparation", edit: "logic.ts"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			sourceFiles(t, root, map[string]string{"package.json": sourcePackage, "server.js": "export default {}"})
			h := newRunHarness(t)
			h.mux.HandleFunc("GET "+protocol.APIPathInfo, func(w http.ResponseWriter, _ *http.Request) {
				installationtest.WriteJSON(w, protocol.Info{
					Installation:          protocol.InstallationReference{Origin: h.server.URL, UID: "test-installation"},
					ImageRepositoryPrefix: "registry.example/apps",
				})
			})
			h.control.Handle([]string{protocol.ToolGetSession}, nil, func(w http.ResponseWriter, _ *http.Request) {
				installationtest.WriteJSON(w, protocol.SessionResponse{})
			})
			h.start(t, func(_ context.Context, _ buildRequest, progress io.Writer) (string, error) {
				if tc.edit != "" {
					err := os.WriteFile(filepath.Join(root, tc.edit), []byte("edited"), 0o644)
					return "superseded", err
				}
				_, _ = io.WriteString(progress, tc.diagnostics+"\n")
				return "", errors.New("fixture build failed")
			})
			var diagnostics bytes.Buffer
			err := executeCommand(t.Context(), []string{tc.command, "alpha=" + root, tc.access}, nil, &diagnostics, h.run)
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(diagnostics.String(), tc.diagnostics) {
				t.Fatalf("error=%v diagnostics=%q, want %q", err, diagnostics.String(), tc.want)
			}
		})
	}
}

func stepUntil(t *testing.T, fc *testingclock.FakeClock, step time.Duration, observed func() bool) {
	t.Helper()
	if err := wait.PollUntilContextTimeout(t.Context(), 5*time.Millisecond, 10*time.Second, true, func(context.Context) (bool, error) {
		if observed() {
			return true, nil
		}
		fc.Step(step)
		return false, nil
	}); err != nil {
		t.Fatalf("condition was not observed: %v", err)
	}
}

type runHarness struct {
	mux      *http.ServeMux
	control  *installationtest.ControlFixtures
	server   *httptest.Server
	baseHost string
	run      commandConfig
}

func newRunHarness(t *testing.T) *runHarness {
	t.Helper()
	h := &runHarness{mux: http.NewServeMux()}
	h.control = installationtest.NewControlFixtures(t, h.mux)
	installationtest.MountDeviceAuth(t, h.mux, func() string { return h.server.URL + "/" }, protocol.AdminPlane,
		func(r *http.Request) { installationtest.AssertOrigin(t, r, h.baseHost) }, 3600, 0, nil)
	return h
}

func (h *runHarness) start(t *testing.T, build buildApplication) {
	t.Helper()
	h.mux.HandleFunc("GET /sessions/{id}/logs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(protocol.HeaderContentType, protocol.MIMETextPlain)
		w.WriteHeader(http.StatusOK)
		_ = http.NewResponseController(w).Flush()
		<-r.Context().Done()
	})
	h.server = httptest.NewTLSServer(h.mux)
	t.Cleanup(h.server.Close)
	h.baseHost = strings.TrimPrefix(h.server.URL, "https://")
	stateDir := t.TempDir()
	auth := ClientConfig{
		ConfigDir:  stateDir,
		HTTPClient: installationtest.OriginClient(h.server), RequestTimeout: time.Second,
	}
	manager := newManager(t, h.server, stateDir)
	if manager.info.OAuth != nil {
		seedAdminCredentials(t, manager, oauth2.Token{
			AccessToken: "access-token", RefreshToken: "refresh-token",
			TokenType: "Bearer", Expiry: time.Now().Add(time.Hour),
		})
	}
	sourceFiles(t, stateDir, map[string]string{"config.json": fmt.Sprintf(`{"origin":%q}`, h.server.URL)})
	_, registry := buildRegistry(t)
	registryHost := strings.TrimPrefix(registry.URL, "https://")
	if build == nil {
		build = func(context.Context, buildRequest, io.Writer) (string, error) {
			return "", errors.New("fixture build failed")
		}
	}
	h.run = commandConfig{
		builder: build,
		RegistryHTTPClient: installationtest.HTTPClient(func(r *http.Request) (*http.Response, error) {
			original := r
			r = r.Clone(r.Context())
			if r.URL.Host == "registry.example" {
				r.URL.Host = registryHost
				r.Host = registryHost
			}
			response, err := registry.Client().Transport.RoundTrip(r)
			if response != nil {
				response.Request = original
			}
			return response, err
		}),
		Auth: auth,
		Env:  []string{"DOCKER_HOST=unix:///tmp/holon-test.sock"},
	}
}

func startDev(t *testing.T, config commandConfig, args ...string) (stdout, stderr *installationtest.ObservedOutput, stop func() error) {
	ctx, cancel := context.WithCancel(t.Context())
	stdout, stderr = &installationtest.ObservedOutput{OnWrite: func(string) {}}, &installationtest.ObservedOutput{OnWrite: func(string) {}}
	done := make(chan error, 1)
	go func() { done <- executeCommand(ctx, append([]string{"dev"}, args...), stdout, stderr, config) }()
	return stdout, stderr, func() error {
		cancel()
		return <-done
	}
}

func TestRunDevLifecycle(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	sourceFiles(t, root, map[string]string{
		"alpha/server.js": "export default {}", "alpha/package.json": sourcePackage,
		"beta/server.js": "export default {}", "beta/package.json": sourcePackage,
	})
	for _, name := range []string{"alpha", "beta"} {
		writeDescriptor(t, filepath.Join(root, name, "server.json"), descriptorDocument(name))
	}
	firstBuild, nextBuild := buildImage(t, "first"), buildImage(t, "next")
	build := func(ctx context.Context, request buildRequest, progress io.Writer) (string, error) {
		if !slices.Equal(request.Platforms, []string{"linux/arm64"}) {
			t.Errorf("session platforms = %v", request.Platforms)
		}
		switch edit, _ := os.ReadFile(filepath.Join(root, "alpha", "logic.txt")); string(edit) {
		case "":
			return firstBuild(ctx, request, progress)
		case "broken":
			return "", errors.New("rebuild failed")
		case "superseded":
			if err := os.WriteFile(filepath.Join(root, "alpha", "logic.txt"), []byte("fixed"), 0o644); err != nil {
				return "", err
			}
		}
		return nextBuild(ctx, request, progress)
	}
	var mu sync.Mutex
	var ambiguous, unavailable bool
	var waiting string
	var creates int
	var created, served []protocol.App
	var updates [][]protocol.App
	outage := sets.New[string]()
	h := newRunHarness(t)
	h.mux.HandleFunc("GET "+protocol.APIPathInfo, func(w http.ResponseWriter, _ *http.Request) {
		installationtest.WriteJSON(w, protocol.Info{
			Installation: protocol.InstallationReference{Origin: h.server.URL, UID: "test-installation"},
			Version:      "test", BuildArchitectures: []string{"arm64"}, OAuth: &protocol.OAuthInfo{
				Issuer: h.server.URL + "/", AdminAudience: installationtest.AdminAudience(h.server.URL), CLIClientID: "cli-test",
			},
		})
	})
	session := func() map[string]any {
		response := apiSession("alpha", "beta")
		response["apps"], response["buildArchitectures"] = served, []string{"arm64"}
		if waiting != "" {
			response["conditions"] = []protocol.Condition{{
				Type: protocol.ConditionReady, Status: "False", Reason: waiting, Message: "Configure API_KEY to start the session.",
			}}
		}
		return response
	}
	h.control.Handle([]string{protocol.ToolUploadGrant}, nil, func(w http.ResponseWriter, r *http.Request) {
		installationtest.AssertOrigin(t, r, "dev."+h.baseHost)
		installationtest.WriteJSON(w, protocol.UploadGrant{
			Grant: "grant-secret", Registry: "registry.example",
			Repositories: map[string]string{"alpha": "holon/upload/alpha", "beta": "holon/upload/beta"},
		})
	})
	h.control.Handle([]string{protocol.ToolCreateSession}, nil, func(w http.ResponseWriter, r *http.Request) {
		var input protocol.AppsRequestFor[protocol.App]
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		mu.Lock()
		defer mu.Unlock()
		creates++
		created, served = input.Apps, slices.Clone(input.Apps)
		if !ambiguous {
			installationtest.WriteJSON(w, session())
			return
		}
		connection, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_ = connection.Close()
	})
	h.control.Handle([]string{toolReloadApp}, nil, func(w http.ResponseWriter, r *http.Request) {
		installationtest.WriteJSON(w, reloadAppResult{ElapsedSeconds: 0.5})
	})
	h.control.Handle([]string{protocol.ToolGetSession, protocol.ToolRenewSession, protocol.ToolUpdateSession},
		map[string]string{"session": "gentle-otter-482"}, func(w http.ResponseWriter, r *http.Request) {
			installationtest.AssertOrigin(t, r, "dev."+h.baseHost)
			mu.Lock()
			defer mu.Unlock()
			switch tool := installationtest.ToolName(r); {
			case tool == protocol.ToolUpdateSession:
				var input protocol.UpdateSessionRequestFor[protocol.AppsRequestFor[protocol.App]]
				if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
					t.Error(err)
				}
				for _, app := range input.Desired.Apps {
					served[slices.IndexFunc(served, func(old protocol.App) bool { return old.Name == app.Name })] = app
				}
				updates = append(updates, input.Desired.Apps)
			case unavailable:
				outage.Insert(tool)
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			installationtest.WriteJSON(w, session())
		})
	const appOutput = "2026-11-09T10:00:00.1Z  alpha starting boom\n2026-11-09T10:00:00.2Z ERROR alpha " + traceback + "\n"
	h.mux.HandleFunc("GET /sessions/gentle-otter-482/logs", func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if query.Has("event") { // Every record, not only MCP calls.
			t.Errorf("log stream query = %s", r.URL.RawQuery)
		}
		if query.Has("since") { // The resumed stream fails, so the stream stops.
			w.WriteHeader(http.StatusPreconditionFailed)
			return
		}
		_, _ = io.WriteString(w, appOutput)
	})
	h.start(t, build)
	t.Run("ambiguous creation is not retried or deleted", func(t *testing.T) {
		mu.Lock()
		ambiguous = true
		mu.Unlock()
		err := executeCommand(t.Context(), []string{"dev", "alpha=alpha", "beta", "--public"}, nil, nil, h.run)
		mu.Lock()
		defer mu.Unlock()
		ambiguous = false
		if err == nil || !strings.Contains(err.Error(), "outcome is unknown; inspect sessions at https://dev."+h.baseHost+" before retrying") ||
			creates != 1 {
			t.Fatalf("ambiguous creation: creates=%d error=%v", creates, err)
		}
	})
	t.Run("session survives configuration, failed rebuilds, edits and outages", func(t *testing.T) {
		mu.Lock()
		waiting = "WaitingForConfiguration"
		mu.Unlock()
		fc := testingclock.NewFakeClock(time.Now())
		config := h.run
		config.Clock = fc
		stdout, stderr, stop := startDev(t, config, "--public")
		await := func(output *installationtest.ObservedOutput, texts ...string) {
			t.Helper()
			stepUntil(t, fc, devDebounce, func() bool {
				return !slices.ContainsFunc(texts, func(text string) bool { return !strings.Contains(output.String(), text) })
			})
		}
		await(stderr, "Configure API_KEY to start the session.", "log stream stopped")
		await(stdout, appOutput)
		sourceFiles(t, root, map[string]string{"alpha/logic.txt": "broken"})
		await(stderr, "remote image preparation failed")
		mu.Lock()
		waiting = ""
		mu.Unlock()
		await(stdout, "Watching for changes.")
		document := descriptorDocument("alpha")
		document["packages"].([]any)[0].(map[string]any)["transport"].(map[string]any)["url"] = "http://localhost:8080/mcp"
		writeDescriptor(t, filepath.Join(root, "alpha", "server.json"), document)
		await(stderr, "restart holon dev")
		sourceFiles(t, config.Auth.ConfigDir, map[string]string{"config.json": `{"origin":"https://other.example"}`})
		sourceFiles(t, root, map[string]string{"alpha/logic.txt": "superseded"})
		await(stderr, "remote update requested")
		mu.Lock()
		unavailable = true
		mu.Unlock()
		stepUntil(t, fc, renewalInterval, func() bool {
			mu.Lock()
			defer mu.Unlock()
			return outage.HasAll(protocol.ToolGetSession, protocol.ToolRenewSession)
		})
		mu.Lock()
		unavailable = false
		mu.Unlock()
		if err := stop(); err != nil {
			t.Fatalf("run: %v stderr=%q", err, stderr.String())
		}
		mu.Lock()
		defer mu.Unlock()
		if len(updates) != 1 || len(updates[0]) != 1 || updates[0][0].Name != "alpha" || updates[0][0].Image == created[0].Image {
			t.Fatalf("source edit updates = %#v", updates)
		}
		if strings.Contains(stdout.String()+stderr.String(), "grant-secret") {
			t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
		}
	})
}

func TestDevDeploymentStatusTransitions(t *testing.T) {
	h := newRunHarness(t)
	h.mux.HandleFunc("GET "+protocol.APIPathInfo, func(w http.ResponseWriter, _ *http.Request) {
		installationtest.WriteJSON(w, protocol.Info{Installation: protocol.InstallationReference{Origin: h.server.URL, UID: "test-installation"}})
	})
	responses := make(chan protocol.SessionResponse, 1)
	h.control.Handle([]string{protocol.ToolGetSession}, nil, func(w http.ResponseWriter, _ *http.Request) {
		installationtest.WriteJSON(w, <-responses)
	})
	h.start(t, nil)
	manager, err := NewClient(t.Context(), h.run.Auth)
	if err != nil {
		t.Fatal(err)
	}
	var stream bytes.Buffer
	output, _, _ := newProgressOutput(io.Discard, &stream, false, h.run.clock())
	dev := devLoop{
		manager: manager, name: "gentle-otter-482", clock: h.run.clock(),
		options: buildOptions{output: output},
	}
	for _, ready := range []string{"False", "True"} {
		responses <- protocol.SessionResponse{Generation: 1, Conditions: []protocol.Condition{
			{Type: protocol.ConditionReady, Status: ready, Reason: protocol.ReasonProgressDeadline, Message: "rollout failed"},
			{Type: protocol.ConditionAvailable, Status: "True"},
		}}
		if err := dev.poll(manager.Session(t.Context(), dev.name)); err != nil {
			t.Fatal(err)
		}
	}
	want := "remote deployment remote update failed; existing service remains available (generation 1): ProgressDeadlineExceeded: rollout failed\n" +
		"remote deployment ok\n"
	if !strings.HasSuffix(stream.String(), want) {
		t.Fatalf("deployment reports = %q, want %q", stream.String(), want)
	}
}

func TestDevStopsWhenReauthenticationIsRequired(t *testing.T) {
	t.Parallel()
	server := newAuthTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.FormValue("grant_type") == "refresh_token" {
			w.Header().Set(protocol.HeaderContentType, protocol.MIMEApplicationJSON)
			w.WriteHeader(http.StatusBadRequest)
			installationtest.WriteJSON(w, map[string]string{"error": "invalid_grant"})
			return
		}
		installationtest.WriteJSON(w, map[string]any{"token_type": "Bearer", "access_token": "access", "refresh_token": "refresh"})
	})
	manager := newManager(t, server, t.TempDir())
	if err := manager.loginInteractive(t.Context()); err != nil {
		t.Fatal(err)
	}
	dev := devLoop{manager: manager, name: "gentle-otter-482"}
	if err := dev.poll(manager.Session(t.Context(), dev.name)); !errors.Is(err, errReauthenticationRequired) {
		t.Fatalf("poll = %v, want %v", err, errReauthenticationRequired)
	}
}

func apiSession(names ...string) map[string]any {
	response := installationtest.SessionResponse("gentle-otter-482", "")
	apps := make([]protocol.App, 0, len(names))
	for _, name := range names {
		apps = append(apps, protocol.App{Name: name})
	}
	response["apps"] = apps
	return response
}

func TestRunDevRemoteWithoutBuildContext(t *testing.T) {
	remote := "apps:\n- name: alpha\n  remote: {url: https://upstream.example/mcp}\n  access: {server: {}}\n"
	selection := "apps:\n- name: unused\n  path: ./not-checked-out\n  access: {server: {}}\n"
	var dependency *protocol.RegistrySource
	var dependencyName string
	requires := map[string]any{"delta": map[string]string{"url": "https://upstream.example/mcp"}}
	for _, name := range []string{"gamma", "beta", "alpha"} {
		document := descriptorDocument(name)
		delete(document, "packages")
		document["remotes"] = []any{map[string]any{"type": "streamable-http", "url": "https://upstream.example/mcp"}}
		document["_meta"] = map[string]any{mcpregistry.PublisherMetadata: map[string]any{"sh.holon": map[string]any{"requires": requires}}}
		if dependency != nil {
			data, _ := json.Marshal(dependency)
			selection += fmt.Sprintf("- name: %s\n  registry: %s\n  access: {server: {}}\n", dependencyName, data)
		}
		dependency, dependencyName = descriptorRegistry(t, document), name
		requires = map[string]any{name: dependency.RegistryRef}
	}
	alpha := "alpha=" + dependency.Registry + "/v0.1/servers/" + url.PathEscape(dependency.Name) + "/versions/" + dependency.Version
	for _, tc := range []struct {
		name, file, manifest string
		apps, want           []string
		oauth                *protocol.OAuthClient
	}{
		{
			name: "default manifest with OAuth client", file: manifestFileName, want: []string{"alpha"},
			manifest: remote + "  oauth: {clientID: '{CLIENT_ID}', clientSecret: {secret: CLIENT_SECRET}}\n",
			oauth:    &protocol.OAuthClient{ClientID: "{CLIENT_ID}", ClientSecret: "CLIENT_SECRET"},
		},
		{name: "yml fallback", file: "holon.yml", manifest: remote, want: []string{"alpha"}},
		{
			name: "command-line sources, file dependencies and repeated names", file: manifestFileName, manifest: selection,
			apps: []string{"delta=https://upstream.example/mcp", alpha, "delta", "--public"}, want: []string{"alpha", "beta", "delta", "gamma"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			sourceFiles(t, root, map[string]string{"holon.yml": "invalid manifest"})
			sourceFiles(t, root, map[string]string{tc.file: tc.manifest})
			h := newRunHarness(t)
			h.control.Handle([]string{protocol.ToolCreateSession, protocol.ToolGetSession}, nil, func(w http.ResponseWriter, r *http.Request) {
				switch installationtest.ToolName(r) {
				case protocol.ToolCreateSession:
					var input protocol.AppsRequestFor[protocol.App]
					if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
						t.Error(err)
					}
					var names []string
					for _, app := range input.Apps {
						names = append(names, app.Name)
					}
					slices.Sort(names)
					app := input.Apps[0]
					if !slices.Equal(names, tc.want) || app.Remote == nil || app.Remote.URL != "https://upstream.example/mcp" ||
						app.Image != "" || app.Port != 0 || !reflect.DeepEqual(app.OAuth, tc.oauth) {
						t.Errorf("unexpected remote session: %#v", input)
					}
					for name := range app.OAuth.Inputs() {
						if _, injected := app.Env[name]; injected {
							t.Error("OAuth input injected into the application environment")
						}
					}
				}
				installationtest.WriteJSON(w, apiSession(tc.want...))
			})
			h.start(t, nil)
			fc := testingclock.NewFakeClock(time.Now())
			h.run.Clock = fc
			stdout, _, stop := startDev(t, h.run, tc.apps...)
			stepUntil(t, fc, workflowPoll, func() bool { return strings.Contains(stdout.String(), "/mcp/alpha") })
			if err := stop(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRunDeployLifecycle(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	sourceFiles(t, root, map[string]string{"server.js": "export default {}"})
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(sourcePackage), 0o644); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(root, "holon.yaml")
	if err := os.WriteFile(manifestPath, []byte(`apps:
  - name: hosted
    remote: {url: https://mcp.example/mcp}
    access: {server: {}}
  - name: prod
    mount: /
    access: {server: {}}
    path: .
`), 0o644); err != nil {
		t.Fatal(err)
	}
	buildClock := testingclock.NewFakeClock(time.Unix(100, 0))
	build := func(ctx context.Context, request buildRequest, progress io.Writer) (string, error) {
		if !slices.Equal(request.Platforms, []string{"linux/amd64"}) {
			t.Errorf("production platforms = %v", request.Platforms)
		}
		buildClock.Step(2 * time.Second)
		return buildImage(t, "first")(ctx, request, progress)
	}
	var mu sync.Mutex
	var existing bool
	var desired protocol.AppsRequestFor[protocol.App]
	var written []protocol.App
	var forceConflict, loseResponse bool
	var rolloutFailure string
	revision := 7
	h := newRunHarness(t)
	h.mux.HandleFunc("GET "+protocol.APIPathInfo, func(w http.ResponseWriter, _ *http.Request) {
		installationtest.WriteJSON(w, protocol.Info{
			Installation: protocol.InstallationReference{Origin: h.server.URL, UID: "test-installation"},
			Version:      "test", BuildArchitectures: []string{"amd64"}, OAuth: &protocol.OAuthInfo{
				Issuer: h.server.URL + "/", AdminAudience: installationtest.AdminAudience(h.server.URL), CLIClientID: "cli-test",
			},
		})
	})
	h.control.Handle([]string{protocol.ToolUploadGrant}, nil, func(w http.ResponseWriter, r *http.Request) {
		installationtest.AssertOrigin(t, r, "admin."+h.baseHost)
		var input protocol.UploadRequest
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil || len(input.Apps) == 0 {
			t.Fatalf("production upload = %#v, %v", input, err)
		}
		installationtest.WriteJSON(
			w,
			map[string]any{
				"grant":        "grant-secret",
				"registry":     "registry.example",
				"repositories": map[string]string{"prod": "holon/upload/prod", "hosted": "unused"},
				"expiresAt":    time.Now().Add(time.Hour).Format(time.RFC3339),
			},
		)
	})
	h.control.Handle([]string{protocol.ToolGetSession, protocol.ToolUpdateSession}, map[string]string{"session": "production"},
		func(w http.ResponseWriter, r *http.Request) {
			installationtest.AssertOrigin(t, r, "admin."+h.baseHost)
			mu.Lock()
			defer mu.Unlock()
			switch installationtest.ToolName(r) {
			case protocol.ToolGetSession:
				if !existing {
					http.NotFound(w, r)
					return
				}
				generation := int64(revision)
				conditions := []protocol.Condition{{Type: protocol.ConditionReady, Status: "True", Reason: "Ready"}}
				if rolloutFailure != "" {
					conditions = []protocol.Condition{
						{Type: protocol.ConditionReady, Status: "False", Reason: protocol.ReasonProgressDeadline, Message: "prod: unready replacement"},
						{Type: protocol.ConditionAvailable, Status: "True"},
					}
					if rolloutFailure == "superseded" {
						generation++
						desired.Apps[1].Port = 4001
					}
				}
				installationtest.WriteJSON(w, protocol.SessionResponse{
					Name: "production", UID: "production-uid", Apps: desired.Apps, Generation: generation, Revision: strconv.Itoa(revision),
					Conditions: conditions,
				})
			case protocol.ToolUpdateSession:
				var input protocol.UpdateSessionRequestFor[protocol.AppsRequestFor[protocol.App]]
				if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
					t.Error(err)
					return
				}
				if forceConflict {
					forceConflict = false
					for i := range desired.Apps {
						if desired.Apps[i].Name == "prod" {
							desired.Apps[i].Port = 4000
						}
					}
					revision++
				}
				if existing && input.ExpectedRevision != strconv.Itoa(revision) {
					w.WriteHeader(http.StatusPreconditionFailed)
					return
				}
				if !existing && !input.CreateOnly {
					w.WriteHeader(http.StatusPreconditionFailed)
					return
				}
				written = input.Desired.Apps
				desired.Apps = append(slices.DeleteFunc(desired.Apps, func(app protocol.App) bool {
					return slices.ContainsFunc(written, func(update protocol.App) bool { return update.Name == app.Name })
				}), written...)
				existing = true
				revision++
				if loseResponse {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				installationtest.WriteJSON(w, protocol.SessionResponse{
					Name: "production", UID: "production-uid", Apps: desired.Apps, Generation: int64(revision), Revision: strconv.Itoa(revision),
					Conditions: []protocol.Condition{{Type: protocol.ConditionReady, Status: "False", Reason: "Provisioning"}},
				})
			}
		})
	h.start(t, build)
	runConfig := h.run
	runConfig.Clock = buildClock
	var output, diagnostics bytes.Buffer
	if err := executeCommand(t.Context(), []string{"deploy", "--project", manifestPath}, &output, &diagnostics, runConfig); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	initial := slices.Clone(desired.Apps)
	mu.Unlock()
	if slices.ContainsFunc(initial, func(app protocol.App) bool { return app.Overlay != "" }) {
		t.Fatalf("deploy carries an overlay: %+v", initial)
	}
	if strings.Contains(diagnostics.String(), "fixture build output") ||
		strings.Contains(diagnostics.String(), "Timing ") ||
		!strings.Contains(diagnostics.String(), "prod ready") || strings.ContainsAny(diagnostics.String(), "\x1b\r") {
		t.Fatalf("compact deployment output: %q", diagnostics.String())
	}
	// The summary names the remote app without a page, and nothing else production runs.
	wantOutput := "Ready MCP tools (all apps) https://" + h.baseHost + "/mcp hosted MCP https://" + h.baseHost + "/mcp/hosted " +
		"prod Browser https://" + h.baseHost + "/ MCP https://" + h.baseHost + "/mcp/prod"
	if strings.Join(strings.Fields(output.String()), " ") != wantOutput {
		t.Fatalf("endpoint = %q", output.String())
	}
	output.Reset()
	diagnostics.Reset()
	mu.Lock()
	desired.Apps[1].Image = strings.Replace(initial[1].Image, "registry.example/holon/upload/prod", "old-registry.example/prod", 1)
	mu.Unlock()
	if err := executeCommand(t.Context(), []string{"deploy", "--project", manifestPath, "--verbose"}, &output, &diagnostics, runConfig); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diagnostics.String(), "fixture build output") ||
		!strings.Contains(diagnostics.String(), "Timing apps prepare contracts: ") ||
		!strings.Contains(diagnostics.String(), "Timing prod build and upload: 2s") {
		t.Fatalf("verbose deployment hid build output or timings: %q", diagnostics.String())
	}
	mu.Lock()
	if desired.Apps[1].Image != initial[1].Image {
		t.Errorf("deployment retained an obsolete repository: %s", desired.Apps[1].Image)
	}
	forceConflict = true
	mu.Unlock()
	err := executeCommand(t.Context(), []string{"deploy", "--project", manifestPath}, &output, nil, runConfig)
	if err == nil || !strings.Contains(err.Error(), "concurrently") {
		t.Fatalf("conditional conflict = %v", err)
	}
	for _, scenario := range []struct{ name, message string }{
		{"progress deadline", "ProgressDeadlineExceeded: prod: unready replacement"},
		{"superseded", "superseded"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			mu.Lock()
			forceConflict, rolloutFailure = false, scenario.name
			mu.Unlock()
			output.Reset()
			err := executeCommand(t.Context(), []string{"deploy", "--project", manifestPath}, &output, nil, runConfig)
			if err == nil || !strings.Contains(err.Error(), scenario.message) {
				t.Fatalf("rollout result: %v", err)
			}
			if output.Len() != 0 {
				t.Fatalf("failed rollout printed endpoints: %q", output.String())
			}
		})
	}
	updated := "hosted=https://updated.example/mcp"
	for _, scenario := range []struct {
		name   string
		args   []string
		create bool
	}{
		{name: "selected creation", args: []string{updated, "--public"}, create: true},
		{name: "disjoint update", args: []string{updated}},
		{name: "lost accepted response", args: []string{updated}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			mu.Lock()
			desired = protocol.AppsRequestFor[protocol.App]{Apps: slices.Clone(initial)}
			existing, rolloutFailure = !scenario.create, ""
			forceConflict = scenario.name == "disjoint update"
			loseResponse = scenario.name == "lost accepted response"
			mu.Unlock()
			runConfig := h.run
			runConfig.RegistryHTTPClient = installationtest.HTTPClient(func(*http.Request) (*http.Response, error) {
				return nil, errors.New("no access to unrelated application images")
			})
			err := executeCommand(t.Context(), append([]string{"deploy"}, scenario.args...), nil, nil, runConfig)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			if len(written) != 1 || written[0].Name != "hosted" || written[0].Remote == nil ||
				written[0].Remote.URL != "https://updated.example/mcp" {
				t.Fatalf("selected deployment wrote %#v, want only the updated hosted app", written)
			}
		})
	}
}

func TestStreamLogsResumesAfterOutages(t *testing.T) {
	t.Parallel()
	const (
		first  = "2026-11-07T10:00:00.1Z INFO alpha started\n"
		second = "2026-11-07T10:00:00.2Z INFO alpha multi-line\n  continued\n"
		third  = "2026-11-07T10:00:00.3Z INFO alpha resumed\n"
	)
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		var refreshes int
		var since []string
		mux := http.NewServeMux()
		installationtest.MountDeviceAuth(t, mux, func() string { return "https://apps.example.test/" }, protocol.AdminPlane, nil, 0, 0,
			func(w http.ResponseWriter, _ *http.Request) {
				if refreshes++; refreshes == 1 {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				installationtest.WriteJSON(w, map[string]any{"token_type": "Bearer", "access_token": "access", "expires_in": 3600})
			})
		mux.HandleFunc("GET /sessions/alpha/logs", func(w http.ResponseWriter, r *http.Request) {
			since = append(since, r.URL.Query().Get("since"))
			w.Header().Set(protocol.HeaderContentType, protocol.MIMETextPlain)
			body := first + second
			if len(since) > 1 {
				body += third // The server replays its backlog from since.
			}
			_, _ = io.WriteString(w, body)
		})
		transport := installationtest.HTTPClient(func(r *http.Request) (*http.Response, error) {
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, r)
			return response.Result(), nil
		})
		client, err := NewClient(t.Context(), ClientConfig{Origin: "https://apps.example.test", ConfigDir: t.TempDir(), HTTPClient: transport})
		if err != nil {
			t.Fatal(err)
		}
		seedAdminCredentials(t, client, oauth2.Token{AccessToken: "expired", RefreshToken: "refresh", TokenType: "Bearer", Expiry: time.Now()})
		output := &installationtest.ObservedOutput{OnWrite: func(text string) {
			if strings.HasSuffix(text, third) {
				cancel()
			}
		}}
		input := protocol.SessionLogsRequest{AppRequest: protocol.AppRequest{SessionRequest: protocol.SessionRequest{Session: "alpha"}}, Since: "5m"}
		start := time.Now()
		if err := client.StreamLogs(ctx, input, output); !errors.Is(err, context.Canceled) {
			t.Fatalf("stream error = %v", err)
		}
		// One second after the failed refresh, then one after the dropped stream.
		if elapsed := time.Since(start); elapsed != 2*time.Second || refreshes != 2 || !slices.Equal(since, []string{"5m", "2026-11-07T10:00:00.2Z"}) {
			t.Fatalf("resumed after %v with %d refreshes and since = %q", elapsed, refreshes, since)
		}
		if got := output.String(); got != first+second+third {
			t.Fatalf("output = %q", got)
		}
	})
}
