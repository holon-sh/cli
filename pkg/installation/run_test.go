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
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sharedcli "github.com/holon-sh/cli/pkg/cli"
	"github.com/holon-sh/cli/pkg/mcpregistry"
	"github.com/holon-sh/cli/pkg/typegen"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/oauth2"

	"k8s.io/apimachinery/pkg/util/wait"
	testingclock "k8s.io/utils/clock/testing"

	"github.com/holon-sh/cli/pkg/installation/installationtest"
	"github.com/holon-sh/protocol"
)

func executeCommand(ctx context.Context, args []string, stdout, stderr io.Writer, config commandConfig) error {
	config.Auth.Env = append([]string{"HOLON_ORIGIN="}, config.Auth.Env...)
	command := sharedcli.NewCommand("dev")
	command.AddCommand(newCommands(config)...)
	command.SetArgs(args)
	command.SetOut(cmp.Or(stdout, io.Discard))
	command.SetErr(cmp.Or(stderr, io.Discard))
	return command.ExecuteContext(ctx)
}

func TestRunRejectsInvalidInputs(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	sourceFiles(t, root, map[string]string{manifestFileName: manifestFixture})
	for want, args := range map[string][]string{
		`unknown app "missing"`:                    {"dev", "alpha", "missing", "--project", filepath.Join(root, manifestFileName)},
		"--env must be dev or prod":                {"config", "set", "--env", "staging", "API_URL", "value"},
		"--session requires a development session": {"config", "set", "--session", "production", "API_URL", "value"},
		`invalid variable name "not-valid"`:        {"config", "set", "not-valid", "value"},
	} {
		config := commandConfig{Auth: ClientConfig{ConfigDir: t.TempDir()}}
		if err := executeCommand(t.Context(), args, nil, nil, config); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("invalid command %v: error=%v, want %q", args, err, want)
		}
	}
}

func TestRunPreparationFailures(t *testing.T) {
	t.Parallel()
	for _, command := range []string{"dev", "deploy"} {
		for _, tc := range []struct{ name, access, want string }{
			{name: "build failure", access: `{"server":{}}`},
			{name: "missing identity", access: `{"server":{},"tools":{"read":{"oauth":{}}}}`, want: "requires a configured identity provider"},
		} {
			t.Run(command+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				root := t.TempDir()
				sourceFiles(t, root, map[string]string{
					"package.json": sourcePackage, "server.js": "export default {}",
					manifestFileName: "apps:\n- name: alpha\n" +
						"  path: .\n  access: " + tc.access + "\n",
				})
				h := newRunHarness(t)
				h.mux.HandleFunc("GET "+protocol.APIPathInfo, func(w http.ResponseWriter, _ *http.Request) {
					installationtest.WriteJSON(w, protocol.Info{Installation: protocol.InstallationReference{Origin: h.server.URL, UID: "test-installation"}})
				})
				h.control.Handle([]string{protocol.ToolGetSession}, nil, func(w http.ResponseWriter, _ *http.Request) {
					installationtest.WriteJSON(w, protocol.SessionResponse{
						Name: "production", UID: "production-uid", Revision: "1",
						Apps: []protocol.App{{Name: "alpha", Image: "registry.example/alpha@sha256:" + strings.Repeat("a", 64)}},
					})
				})
				h.start(t, func(_ context.Context, _ buildRequest, progress io.Writer) (buildResult, error) {
					_, _ = io.WriteString(progress, "missing dependency: fixture-package\n")
					return buildResult{}, errors.New("fixture build failed")
				})
				var diagnostics bytes.Buffer
				err := executeCommand(t.Context(), []string{command, "--project", filepath.Join(root, manifestFileName)}, nil, &diagnostics, h.run)
				if tc.want == "" {
					if err == nil || !strings.Contains(err.Error(), "fixture build failed") || !strings.Contains(diagnostics.String(), "missing dependency: fixture-package") {
						t.Fatalf("build failure was not reported: error=%v diagnostics=%q", err, diagnostics.String())
					}
				} else if !errors.Is(err, protocol.ErrInvalidAccess) || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("want %q before image preparation: error=%v", tc.want, err)
				}
			})
		}
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
	clock := testingclock.NewFakePassiveClock(time.Unix(100, 0))
	stateDir := t.TempDir()
	auth := ClientConfig{
		ConfigDir:  stateDir,
		HTTPClient: installationtest.OriginClient(h.server), now: clock.Now, RequestTimeout: time.Second,
	}
	manager := newManager(t, h.server, clock, stateDir)
	if manager.info.OAuth != nil {
		seedAdminCredentials(t, manager, oauth2.Token{
			AccessToken: "access-token", RefreshToken: "refresh-token",
			TokenType: "Bearer", ExpiresIn: 3600, Expiry: clock.Now().Add(time.Hour),
		})
	}
	sourceFiles(t, stateDir, map[string]string{"config.json": fmt.Sprintf(`{"origin":%q}`, h.server.URL)})
	_, registry := buildRegistry(t)
	registryHost := strings.TrimPrefix(registry.URL, "https://")
	if build == nil {
		build = func(context.Context, buildRequest, io.Writer) (buildResult, error) {
			return buildResult{}, errors.New("fixture build failed")
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
	t.Parallel()
	root := t.TempDir()
	manifestPath := filepath.Join(root, manifestFileName)
	sourceFiles(t, root, map[string]string{
		"alpha/server.js": "export default {}", "alpha/package.json": sourcePackage,
		"beta/server.js": "export default {}", "beta/package.json": sourcePackage,
		"beta/node_modules/package/index.js": "", "beta/.git/config": "",
		manifestFileName: "apps:\n- name: alpha\n  access: {server: {}}\n  path: ./alpha\n- name: beta\n  access: {server: {}}\n  path: ./beta\n",
	})
	for _, name := range []string{"alpha", "beta"} {
		writeDescriptor(t, filepath.Join(root, name, "server.json"), descriptorDocument(name))
	}
	firstBuild, nextBuild := buildImage(t, "first"), buildImage(t, "next")
	var builds atomic.Int32
	build := func(ctx context.Context, request buildRequest, progress io.Writer) (buildResult, error) {
		builds.Add(1)
		if !slices.Equal(request.Platforms, []string{"linux/arm64"}) {
			t.Errorf("session platforms = %v", request.Platforms)
		}
		switch edit, _ := os.ReadFile(filepath.Join(root, "alpha", "logic.ts")); string(edit) {
		case "":
			return firstBuild(ctx, request, progress)
		case "broken":
			return buildResult{}, errors.New("rebuild failed")
		}
		return nextBuild(ctx, request, progress)
	}
	var mu sync.Mutex
	var ambiguous, unavailable bool
	var waiting string
	var creates, deletes int
	var created, served []protocol.App
	var updates [][]protocol.App
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
		response := apiSession(h, "alpha", "beta")
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
			Grant: "grant-secret", Registry: "registry.example", ExpiresAt: time.Now().Add(time.Hour).Format(time.RFC3339),
			Repositories: map[string]string{"alpha": "holon/upload/alpha", "beta": "holon/upload/beta"},
		})
	})
	h.control.Handle([]string{protocol.ToolCreateSession}, nil, func(w http.ResponseWriter, r *http.Request) {
		installationtest.AssertOrigin(t, r, "dev."+h.baseHost)
		var input protocol.AppsRequestFor[protocol.App]
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		mu.Lock()
		defer mu.Unlock()
		creates++
		created, served = input.Apps, input.Apps
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
	h.control.Handle([]string{protocol.ToolGetSession, protocol.ToolRenewSession, protocol.ToolUpdateSession, protocol.ToolDeleteSession},
		map[string]string{"session": "gentle-otter-482"}, func(w http.ResponseWriter, r *http.Request) {
			installationtest.AssertOrigin(t, r, "dev."+h.baseHost)
			mu.Lock()
			defer mu.Unlock()
			switch tool := installationtest.ToolName(r); {
			case tool == protocol.ToolDeleteSession:
				deletes++
				installationtest.WriteJSON(w, protocol.DeleteSessionResultFor[protocol.SessionResponse]{Deleted: true})
				return
			case tool == protocol.ToolUpdateSession:
				var input protocol.UpdateSessionRequestFor[protocol.AppsRequestFor[protocol.App]]
				if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
					t.Error(err)
				}
				served = input.Desired.Apps
				updates = append(updates, served)
			case unavailable:
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			installationtest.WriteJSON(w, session())
		})
	h.mux.HandleFunc("GET /sessions/gentle-otter-482/logs", func(w http.ResponseWriter, r *http.Request) {
		if query := r.URL.Query(); query.Get("event") != "holon.mcp.call" || query.Get("follow") != "true" {
			t.Errorf("request stream query = %s", r.URL.RawQuery)
		}
		w.Header().Set(protocol.HeaderContentType, protocol.MIMETextPlain)
		_, _ = io.WriteString(w, "2030-01-01T12:00:01Z INFO alpha tools/call echo ok 1ms trace_id=aaaa\n")
	})
	h.start(t, build)
	t.Run("ambiguous creation is not retried or deleted", func(t *testing.T) {
		mu.Lock()
		ambiguous = true
		mu.Unlock()
		err := executeCommand(t.Context(), []string{"dev", "--project", manifestPath}, nil, nil, h.run)
		mu.Lock()
		defer mu.Unlock()
		ambiguous = false
		if err == nil || !strings.Contains(err.Error(), "outcome is unknown; inspect sessions at https://dev."+h.baseHost+" before retrying") ||
			creates != 1 || deletes != 0 {
			t.Fatalf("ambiguous creation: creates=%d deletes=%d error=%v", creates, deletes, err)
		}
	})
	t.Run("session survives configuration, failed rebuilds, edits and outages", func(t *testing.T) {
		mu.Lock()
		waiting = "WaitingForConfiguration"
		mu.Unlock()
		builds.Store(0)
		fc := testingclock.NewFakeClock(time.Now())
		config := h.run
		config.Clock = fc
		stdout, stderr, stop := startDev(t, config, "--project", manifestPath)
		await := func(output *installationtest.ObservedOutput, texts ...string) {
			t.Helper()
			stepUntil(t, fc, devDebounce, func() bool {
				return !slices.ContainsFunc(texts, func(text string) bool { return !strings.Contains(output.String(), text) })
			})
		}
		await(stderr, "Configure API_KEY to start the session.", "request stream stopped")
		if builds.Load() != 2 || strings.Contains(stdout.String(), "/mcp/alpha") {
			t.Fatalf("startup: builds=%d stdout=%q", builds.Load(), stdout.String())
		}
		sourceFiles(t, root, map[string]string{"alpha/logic.ts": "broken"})
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
		sourceFiles(t, root, map[string]string{"beta/node_modules/package/index.js": "changed", "beta/.git/config": "changed", "alpha/logic.ts": "fixed"})
		await(stderr, "remote update requested")
		mu.Lock()
		unavailable = true
		mu.Unlock()
		stepUntil(t, fc, renewalInterval, func() bool {
			return strings.Contains(stderr.String(), "session status retrying:") && strings.Contains(stderr.String(), "session renewal retrying:")
		})
		recovery := len(stderr.String())
		mu.Lock()
		unavailable = false
		mu.Unlock()
		stepUntil(t, fc, renewalInterval, func() bool {
			return strings.Contains(stderr.String()[recovery:], "session status ok") && strings.Contains(stderr.String()[recovery:], "session renewal ok")
		})
		if err := stop(); err != nil {
			t.Fatalf("run: %v stderr=%q", err, stderr.String())
		}
		mu.Lock()
		defer mu.Unlock()
		if len(created) != 2 || !strings.HasPrefix(created[0].Image, "registry.example/holon/upload/alpha@sha256:") || deletes != 1 {
			t.Fatalf("session apps = %#v, deletes = %d", created, deletes)
		}
		if len(updates) != 1 || updates[0][0].Image == created[0].Image || updates[0][1].Image != created[1].Image {
			t.Fatalf("source edit updates = %#v", updates)
		}
		if strings.Count(stdout.String(), "trace_id=aaaa") != 1 || strings.Contains(stdout.String()+stderr.String(), "grant-secret") {
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
		options: buildOptions{output: output, types: &typeWorkspace{prepared: new(typegen.Prepared)}},
	}
	for _, step := range []struct {
		generation int64
		ready      string
	}{
		{1, "False"},
		{1, "False"},
		{2, "False"},
		{2, "True"},
		{2, "True"},
		{2, "False"},
	} {
		responses <- protocol.SessionResponse{Generation: step.generation, Conditions: []protocol.Condition{
			{Type: protocol.ConditionReady, Status: step.ready, Reason: protocol.ReasonProgressDeadline, Message: "rollout failed"},
			{Type: protocol.ConditionAvailable, Status: "True"},
		}}
		if err := dev.poll(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	want := "editor imports ok\nsession status ok\n" +
		"remote deployment remote update failed; existing service remains available (generation 1): ProgressDeadlineExceeded: rollout failed\n" +
		"remote deployment remote update failed; existing service remains available (generation 2): ProgressDeadlineExceeded: rollout failed\n" +
		"remote deployment ready (generation 2)\n" +
		"remote deployment remote update failed; existing service remains available (generation 2): ProgressDeadlineExceeded: rollout failed\n"
	if stream.String() != want {
		t.Fatalf("deployment reports = %q, want %q", stream.String(), want)
	}
}

func apiSession(h *runHarness, names ...string) map[string]any {
	response := installationtest.SessionResponse("gentle-otter-482", "")
	endpoints := make([]protocol.SessionEndpoint, 0, len(names))
	apps := make([]protocol.App, 0, len(names))
	for _, name := range names {
		apps = append(apps, protocol.App{Name: name})
		endpoints = append(endpoints, protocol.SessionEndpoint{Name: name, URL: "https://gentle-otter-482." + h.baseHost + protocol.AppPath(name)})
	}
	response["endpoints"], response["apps"] = endpoints, apps
	return response
}

func TestRunDevImportsImageEdits(t *testing.T) {
	fixture := newImageRegistry()
	first := addTestImage(t, fixture, "amd64", ocispec.ImageConfig{}, nil).Digest.String()
	second := addTestImage(t, fixture, "arm64", ocispec.ImageConfig{}, nil).Digest.String()
	registryServer := httptest.NewTLSServer(fixture)
	defer registryServer.Close()
	registryHost := strings.TrimPrefix(registryServer.URL, "https://")
	root := t.TempDir()
	manifest := func(imageDigest string) map[string]string {
		return map[string]string{
			manifestFileName: "apps:\n- name: alpha\n  access: {server: {}}\n  image: " + registryHost + "/source@" + imageDigest + "\n",
		}
	}
	sourceFiles(t, root, manifest(first))
	h := newRunHarness(t)
	var mu sync.Mutex
	var apps []protocol.App
	var images []string
	h.control.Handle([]string{protocol.ToolUploadGrant}, nil, func(w http.ResponseWriter, _ *http.Request) {
		installationtest.WriteJSON(w, protocol.UploadGrant{
			Registry: registryHost, Grant: "grant-secret", Repositories: map[string]string{"alpha": "holon/upload/echo"},
			ExpiresAt: time.Now().Add(time.Hour).Format(time.RFC3339),
		})
	})
	h.control.Handle([]string{protocol.ToolCreateSession, protocol.ToolGetSession, protocol.ToolRenewSession, protocol.ToolUpdateSession, protocol.ToolDeleteSession},
		nil, func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			var input protocol.UpdateSessionRequestFor[protocol.AppsRequestFor[protocol.App]]
			var err error
			switch installationtest.ToolName(r) {
			case protocol.ToolDeleteSession:
				installationtest.WriteJSON(w, protocol.DeleteSessionResultFor[protocol.SessionResponse]{Deleted: true})
				return
			case protocol.ToolCreateSession:
				err = json.NewDecoder(r.Body).Decode(&input.Desired)
			case protocol.ToolUpdateSession:
				err = json.NewDecoder(r.Body).Decode(&input)
			}
			if err != nil {
				t.Error(err)
			}
			if len(input.Desired.Apps) > 0 {
				apps = input.Desired.Apps
				images = append(images, apps[0].Image)
			}
			response := apiSession(h, "alpha")
			response["apps"] = apps
			installationtest.WriteJSON(w, response)
		})
	h.start(t, nil)
	h.run.Env = append(h.run.Env, "DOCKER_CONFIG="+t.TempDir())
	fc := testingclock.NewFakeClock(time.Now())
	h.run.Clock = fc
	stdout, stderr, stop := startDev(t, h.run, "--project", filepath.Join(root, manifestFileName))
	stepUntil(t, fc, devDebounce, func() bool { return strings.Contains(stdout.String(), "Watching for changes.") })
	sourceFiles(t, root, manifest(second))
	stepUntil(t, fc, devDebounce, func() bool { return strings.Contains(stderr.String(), "remote update requested") })
	if err := stop(); err != nil {
		t.Fatalf("image dev run: %v, stderr=%q", err, stderr.String())
	}
	mu.Lock()
	defer mu.Unlock()
	upload := registryHost + "/holon/upload/echo@"
	if !slices.Equal(images, []string{upload + first, upload + second}) {
		t.Fatalf("session images = %v", images)
	}
}

func TestRunDevRemoteWithoutBuildContext(t *testing.T) {
	remote := "apps:\n- name: alpha\n  remote: {url: https://upstream.example/mcp}\n  access: {server: {}}\n"
	selection := "apps:\n- name: unused\n  path: ./not-checked-out\n  access: {server: {}}\n" +
		"- name: delta\n  remote: {url: https://upstream.example/mcp}\n  access: {server: {}}\n"
	var dependency *protocol.RegistrySource
	var dependencyName string
	for _, name := range []string{"gamma", "beta", "alpha"} {
		document := descriptorDocument(name)
		delete(document, "packages")
		document["remotes"] = []any{map[string]any{"type": "streamable-http", "url": "https://upstream.example/mcp"}}
		if dependency != nil {
			document["_meta"] = map[string]any{mcpregistry.PublisherMetadata: map[string]any{"sh.holon": map[string]any{
				"requires": map[string]protocol.RegistryRef{dependencyName: dependency.RegistryRef},
			}}}
		}
		dependency, dependencyName = descriptorRegistry(t, document), name
		data, _ := json.Marshal(dependency)
		selection += fmt.Sprintf("- name: %s\n  registry: %s\n  access: {server: {}}\n", name, data)
	}
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
			name: "selected apps, dependencies and repeated names", file: manifestFileName, manifest: selection,
			apps: []string{"delta", "alpha", "delta"}, want: []string{"alpha", "beta", "delta", "gamma"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			sourceFiles(t, root, map[string]string{"holon.yml": "invalid manifest"})
			sourceFiles(t, root, map[string]string{tc.file: tc.manifest})
			h := newRunHarness(t)
			h.control.Handle([]string{protocol.ToolCreateSession, protocol.ToolGetSession, protocol.ToolDeleteSession}, nil, func(w http.ResponseWriter, r *http.Request) {
				switch installationtest.ToolName(r) {
				case protocol.ToolDeleteSession:
					installationtest.WriteJSON(w, protocol.DeleteSessionResultFor[protocol.SessionResponse]{Deleted: true})
					return
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
				installationtest.WriteJSON(w, apiSession(h, tc.want...))
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
	build := func(ctx context.Context, request buildRequest, progress io.Writer) (buildResult, error) {
		if !slices.Equal(request.Platforms, []string{"linux/amd64"}) {
			t.Errorf("production platforms = %v", request.Platforms)
		}
		buildClock.Step(2 * time.Second)
		return buildImage(t, "first")(ctx, request, progress)
	}
	var mu sync.Mutex
	var existing bool
	var desired protocol.AppsRequestFor[protocol.App]
	var forceConflict, loseResponse, unsupportedField bool
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

		mu.Lock()
		if forceConflict {
			for i := range desired.Apps {
				if desired.Apps[i].Name == "prod" {
					desired.Apps[i].Port = 4000
				}
			}
			revision++
		}
		mu.Unlock()
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
				session := protocol.SessionResponse{
					Name: "production", UID: "production-uid", Apps: desired.Apps, Generation: generation, Revision: strconv.Itoa(revision),
					Endpoints: []protocol.SessionEndpoint{{Name: "prod", URL: "https://apps.example/mcp/prod"}}, Conditions: conditions,
				}
				if !unsupportedField {
					installationtest.WriteJSON(w, session)
					return
				}
				var served map[string]any
				data, _ := json.Marshal(session)
				_ = json.Unmarshal(data, &served)
				served["apps"].([]any)[2].(map[string]any)["futureField"] = true
				installationtest.WriteJSON(w, served)
			case protocol.ToolUpdateSession:
				var input protocol.UpdateSessionRequestFor[protocol.AppsRequestFor[protocol.App]]
				if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
					t.Error(err)
					return
				}
				if existing && input.ExpectedRevision != strconv.Itoa(revision) {
					w.WriteHeader(http.StatusPreconditionFailed)
					return
				}
				if !existing && !input.CreateOnly {
					w.WriteHeader(http.StatusPreconditionFailed)
					return
				}
				desired = input.Desired
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
	if strings.Contains(diagnostics.String(), "fixture build output") ||
		strings.Contains(diagnostics.String(), "Timing ") ||
		!strings.Contains(diagnostics.String(), "prod ready") || strings.ContainsAny(diagnostics.String(), "\x1b\r") {
		t.Fatalf("compact deployment output: %q", diagnostics.String())
	}
	wantOutput := "Ready MCP tools (all apps) https://apps.example/mcp " +
		"prod Browser https://apps.example/ MCP https://apps.example/mcp/prod"
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
	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []struct {
		name   string
		create bool
	}{
		{name: "selected creation", create: true},
		{name: "disjoint update"},
		{name: "lost accepted response"},
		{name: "retain dependency without checkout"},
		{name: "unsupported retained field"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			manifest := strings.Replace(string(manifest), "https://mcp.example/mcp", "https://updated.example/mcp", 1)
			if scenario.name == "retain dependency without checkout" {
				doc := descriptorDocument("hosted")
				delete(doc, "packages")
				doc["remotes"] = []any{map[string]any{"type": "streamable-http", "url": "https://updated.example/mcp"}}
				doc["_meta"] = map[string]any{mcpregistry.PublisherMetadata: map[string]any{"sh.holon": map[string]any{
					"requires": map[string]protocol.RegistryRef{"prod": {Name: "io.github.acme/prod", Version: "1.4.2"}},
				}}}
				source := descriptorRegistry(t, doc)
				source.Remote = &struct{}{}
				declarations := manifestSpec{
					Apps: []manifestApp{
						{Name: "hosted", Registry: source, Access: &protocol.AppAccess{Server: &protocol.AccessRule{}}},
						{Name: "prod", Path: "./not-checked-out", Access: &protocol.AppAccess{Server: &protocol.AccessRule{}}},
					},
				}
				data, _ := json.Marshal(declarations)
				manifest = string(data)
			}
			if err := os.WriteFile(manifestPath, []byte(manifest), 0o600); err != nil {
				t.Fatal(err)
			}
			var retainedApps []protocol.App
			if err := json.Unmarshal(fmt.Appendf(nil, `[
				{"name":"prod","image":%q,"port":3000,"mount":"/",
				 "icons":[{"src":"https://example.com/icon.png","mimeType":"image/png","sizes":["48x48"]}],
				 "oauth":{"clientID":"{CLIENT_ID}","clientSecret":"CLIENT_SECRET"},
				 "access":{"server":{"oauth":{"scopes":["read"]}},"callers":[],"tools":{"ping":{"oauth":{"scopes":["ping"]}}}},
				 "args":["--label={LABEL}","--empty={EMPTY}"],"requires":["upstream"],"egress":[],
				 "env":{"CLIENT_ID":{"isRequired":true},"CLIENT_SECRET":{"isSecret":true},
				        "LABEL":{"default":"retained"},"EMPTY":{"value":""},"EMPTY_DEFAULT":{"default":""}},
				 "envTemplates":{"APP_LABEL":"{LABEL}"}},
				{"name":"upstream","remote":{"url":"https://upstream.example/mcp","headers":{"Authorization":"Bearer {TOKEN}"}},
				 "access":{"server":{}},"env":{"TOKEN":{"isRequired":true,"isSecret":true}},
				 "egress":[{"host":"api.example","ports":[443]}]}
			]`, initial[1].Image), &retainedApps); err != nil {
				t.Fatal(err)
			}
			if scenario.name == "lost accepted response" {
				retainedApps[1].Egress = nil
			}
			mu.Lock()
			desired = protocol.AppsRequestFor[protocol.App]{Apps: append([]protocol.App{initial[0]}, retainedApps...)}
			existing, rolloutFailure = !scenario.create, ""
			forceConflict = scenario.name == "disjoint update"
			loseResponse = scenario.name == "lost accepted response"
			unsupportedField = scenario.name == "unsupported retained field"
			written := revision
			mu.Unlock()
			runConfig := h.run
			if scenario.name != "retain dependency without checkout" {
				runConfig.RegistryHTTPClient = installationtest.HTTPClient(func(*http.Request) (*http.Response, error) {
					return nil, errors.New("no access to unrelated application images")
				})
			}
			err := executeCommand(t.Context(), []string{"deploy", "hosted", "--project", manifestPath}, nil, nil, runConfig)
			mu.Lock()
			defer mu.Unlock()
			if unsupportedField {
				if err == nil || !strings.Contains(err.Error(), "app upstream: ") || !strings.Contains(err.Error(), "futureField") || revision != written {
					t.Fatalf("unsupported retained field: %v, revision %d after %d", err, revision, written)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			wantApps := []string{"hosted", "prod", "upstream"}
			if scenario.create {
				wantApps = []string{"hosted"}
			}
			var gotApps []string
			for _, app := range desired.Apps {
				gotApps = append(gotApps, app.Name)
			}
			slices.Sort(gotApps)
			if !slices.Equal(gotApps, wantApps) {
				t.Fatalf("selected deployment changed retained topology: got %v, want %v", gotApps, wantApps)
			}
			for _, app := range desired.Apps {
				for _, expected := range retainedApps {
					if app.Name != expected.Name {
						continue
					}
					if forceConflict && app.Name == "prod" {
						expected.Port = 4000
					}
					if !reflect.DeepEqual(app, expected) {
						t.Fatalf("selected deployment changed retained %s: got %#v, want %#v", app.Name, app, expected)
					}
				}
				if app.Name == "hosted" {
					if app.Remote == nil || app.Remote.URL != "https://updated.example/mcp" {
						t.Fatal("selected app was not updated")
					}
					if scenario.name == "retain dependency without checkout" && !slices.Equal(app.Requires, []string{"prod"}) {
						t.Fatal("deployed app lost its retained dependency")
					}
				}
			}
		})
	}
}
