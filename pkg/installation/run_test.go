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
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/fsnotify/fsnotify"
	sharedcli "github.com/holon-sh/cli/pkg/cli"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/oauth2"

	"k8s.io/apimachinery/pkg/util/wait"
	testingclock "k8s.io/utils/clock/testing"

	"github.com/holon-sh/cli/pkg/appartifact"
	"github.com/holon-sh/cli/pkg/installation/installationtest"
	"github.com/holon-sh/cli/pkg/mcpcontract"
	"github.com/holon-sh/cli/pkg/recipe"
	"github.com/holon-sh/protocol"
	"github.com/holon-sh/protocol/runtimecontract"
)

func executeCommand(ctx context.Context, args []string, stdout, stderr io.Writer, config commandConfig) error {
	return executeCommandWithInput(ctx, args, nil, stdout, stderr, config)
}

func executeCommandWithInput(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, config commandConfig) error {
	config.Auth.Env = append([]string{"HOLON_ORIGIN="}, config.Auth.Env...)
	command := sharedcli.NewCommand("dev", nil)
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
		{`unknown app "missing"`, []string{"dev", "alpha", "missing", "--file", project}},
		{"--ttl requires --detach", []string{"dev", "--ttl", "24h", "--file", project}},
		{"--json requires --detach", []string{"dev", "--json", "--file", project}},
		{"--session requires --detach", []string{"dev", "--session", "cli-pr-42", "--file", project}},
		{"whole seconds", []string{"dev", "--detach", "--ttl", "0s", "--file", project}},
		{"whole seconds", []string{"dev", "--detach", "--ttl", "1.5s", "--file", project}},
		{"whole seconds", []string{"dev", "--detach", "--ttl", "192h", "--file", project}},
		{"--env must be dev or prod", []string{"config", "set", "--env", "staging", "chat", "API_URL", "value"}},
		{"--session requires a development session", []string{"config", "set", "--session", "production", "chat", "API_URL", "value"}},
		{`invalid variable name "not-valid"`, []string{"config", "set", "chat", "not-valid", "value"}},
		{`invalid app name "Chat"`, []string{"config", "set", "Chat", "API_URL", "value"}},
		{`invalid session name "production"`, []string{"session", "delete", "production"}},
		{`invalid session name ""`, []string{"dev", "--detach", "--session", "", "--file", project}},
	} {
		config := commandConfig{Auth: ClientConfig{ConfigDir: t.TempDir()}}
		if err := executeCommand(t.Context(), tc.args, nil, nil, config); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("invalid command %v: error=%v, want %q", tc.args, err, tc.want)
		}
	}
}

func TestRunPreparationFailures(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, command, access, pkg, prefix, edit, failure, want string
		files                                                   map[string]string
		contractRan, checkRan                                   *runtimecontract.Ran
		base                                                    *digest.Digest
		session                                                 bool
	}{
		{name: "oauth without identity", command: "dev", access: "--oauth", want: "requires a configured identity provider"},
		{name: "start script", command: "dev", pkg: `{"private":true,"main":"index.js"}`, want: "ship it as an image: app"},
		{name: "registry off", command: "deploy", prefix: "registry.example/apps", want: "ship it as an image: app"},
		{name: "unsupported recipe", command: "deploy", files: map[string]string{"bun.lock": ""}, want: "ship it as an image: app"},
		{
			name: "server under server/", command: "deploy", files: map[string]string{"server.js": "export default {}", "server/server.ts": "// mcp"},
			want: "ship it as an image: app",
		},
		{
			name: "contract diagnostic", command: "deploy", session: true, contractRan: &runtimecontract.Ran{ExitCode: 2, Stderr: "server.js: the server raised ValueError\n"},
			want: "server.js: the server raised ValueError",
		},
		{
			name: "build failure", command: "deploy", session: true,
			failure: `{"error":{"code":"invalid_request","message":"the install or build failed: exit status 1"},"output":"npm error missing: fixture-package\n"}`,
			want:    "build alpha: the install or build failed: exit status 1\nnpm error missing: fixture-package",
		},
		{
			name: "type check", command: "deploy", session: true,
			checkRan: &runtimecontract.Ran{ExitCode: 2, Stdout: "server.js(1,7): error TS2322: Type 'string' is not assignable to type 'number'.\n"},
			want:     "alpha does not type-check against its typed contracts:\nserver.js(1,7): error TS2322",
		},
		{name: "missing base", command: "deploy", session: true, base: new(digest.FromString("missing")), want: "does not hold the development image"},
		{name: "source changed", command: "deploy", session: true, edit: "logic.ts", want: "source changed during preparation"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			sourceFiles(t, root, map[string]string{"package.json": cmp.Or(tc.pkg, sourcePackage), "server.js": `export const mcp = 1`})
			sourceFiles(t, root, tc.files)
			h := newRunHarness(t)
			builds := serveBuilds(t, h)
			builds.failure, builds.contractRan, builds.checkRan = tc.failure, tc.contractRan, tc.checkRan
			if tc.edit != "" {
				builds.duringFreeze = func() { sourceFiles(t, root, map[string]string{tc.edit: "edited"}) }
			}
			if tc.base != nil {
				builds.base = *tc.base
			}
			h.mux.HandleFunc("GET "+protocol.APIPathInfo, func(w http.ResponseWriter, _ *http.Request) {
				installationtest.WriteJSON(w, protocol.Info{
					Installation:          protocol.InstallationReference{Origin: h.server.URL, UID: "test-installation"},
					ImageRepositoryPrefix: tc.prefix,
				})
			})
			h.control.Handle([]string{protocol.ToolGetSession}, nil, func(w http.ResponseWriter, _ *http.Request) {
				installationtest.WriteJSON(w, protocol.SessionResponse{})
			})
			h.control.Handle([]string{protocol.ToolUploadGrant}, nil, func(w http.ResponseWriter, _ *http.Request) {
				installationtest.WriteJSON(w, map[string]any{
					"grant": "grant-secret", "registry": "registry.example",
					"repositories": map[string]string{"alpha": "holon/upload/alpha"}, "expiresAt": time.Now().Add(time.Hour).Format(time.RFC3339),
				})
			})
			h.start(t)
			var stderr bytes.Buffer
			err := executeCommand(t.Context(), []string{tc.command, "alpha=" + root, cmp.Or(tc.access, "--public")}, nil, &stderr, h.run)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error=%v stderr=%q, want %q", err, stderr.String(), tc.want)
			}
			builds.mu.Lock()
			defer builds.mu.Unlock()
			if tc.command == "deploy" && (builds.sessions != 0) != tc.session || builds.deleted != builds.sessions {
				t.Fatalf("build sessions created %d, deleted %d; want one created and deleted: %t", builds.sessions, builds.deleted, tc.session)
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

type fakeWatcher chan fsnotify.Event

func (fakeWatcher) Add(string) error { return nil }

func (w fakeWatcher) Close() error {
	close(w)
	return nil
}

func (w fakeWatcher) events() (<-chan fsnotify.Event, <-chan error) { return w, nil }

type fakeFiles struct {
	mu      sync.Mutex
	watcher fakeWatcher
}

func (f *fakeFiles) watch() (fileWatcher, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.watcher = make(fakeWatcher)
	return f.watcher, nil
}

func (f *fakeFiles) save(t *testing.T, root string, files map[string]string) {
	t.Helper()
	sourceFiles(t, root, files)
	f.mu.Lock()
	defer f.mu.Unlock()
	for name := range files {
		f.watcher <- fsnotify.Event{Name: filepath.Join(root, name), Op: fsnotify.Write}
	}
}

type runHarness struct {
	mux            *http.ServeMux
	control        *installationtest.ControlFixtures
	server         *httptest.Server
	registry       *imageRegistry
	registryServer *httptest.Server
	baseHost       string
	files          fakeFiles
	run            commandConfig
}

func newRunHarness(t *testing.T) *runHarness {
	t.Helper()
	h := &runHarness{mux: http.NewServeMux()}
	h.registry, h.registryServer = buildRegistry(t)
	h.control = installationtest.NewControlFixtures(t, h.mux)
	installationtest.MountDeviceAuth(t, h.mux, func() string { return h.server.URL + "/" },
		func(r *http.Request) { installationtest.AssertOrigin(t, r, h.baseHost) }, 3600, 0, nil)
	return h
}

const servedContract = `{"serverInfo":{"name":"example.com/alpha","version":"1","description":"alpha"},"capabilities":{"tools":{}},"lists":{"tools/list":{"pages":[{"tools":[{"name":"lookup","inputSchema":{"type":"object"}}]}]}}}`

func (h *runHarness) start(t *testing.T) {
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
		Env: []string{"DOCKER_HOST=unix:///tmp/holon-test.sock"}, ConfigDir: stateDir,
		HTTPClient: installationtest.OriginClient(h.server),
	}
	manager := newManager(t, h.server, stateDir)
	if manager.info.OAuth != nil {
		seedAdminCredentials(t, manager, oauth2.Token{
			AccessToken: "access-token", RefreshToken: "refresh-token",
			TokenType: "Bearer", Expiry: time.Now().Add(time.Hour),
		})
	}
	sourceFiles(t, stateDir, map[string]string{"config.json": fmt.Sprintf(`{"origin":%q}`, h.server.URL)})
	registry := h.registryServer
	registryHost := strings.TrimPrefix(registry.URL, "https://")
	h.run = commandConfig{
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
		Auth:  auth,
		watch: h.files.watch,
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

func TestDevDeploymentStatusTransitions(t *testing.T) {
	h := newRunHarness(t)
	h.mux.HandleFunc("GET "+protocol.APIPathInfo, func(w http.ResponseWriter, _ *http.Request) {
		installationtest.WriteJSON(w, protocol.Info{Installation: protocol.InstallationReference{Origin: h.server.URL, UID: "test-installation"}})
	})
	responses := make(chan protocol.SessionResponse, 1)
	h.control.Handle([]string{protocol.ToolGetSession}, nil, func(w http.ResponseWriter, _ *http.Request) {
		installationtest.WriteJSON(w, <-responses)
	})
	h.start(t)
	manager, err := NewClient(t.Context(), h.run.Auth)
	if err != nil {
		t.Fatal(err)
	}
	var stream bytes.Buffer
	output, _, _ := newProgressOutput(io.Discard, &stream, false, h.run.clock())
	dev := devLoop{
		manager: manager, clock: h.run.clock(),
		options: buildOptions{output: output},
	}
	for _, ready := range []string{"False", "True"} {
		responses <- protocol.SessionResponse{Generation: 1, Conditions: []protocol.Condition{
			{Type: protocol.ConditionReady, Status: ready, Reason: protocol.ReasonProgressDeadline, Message: "rollout failed"},
			{Type: protocol.ConditionAvailable, Status: "True"},
		}}
		if err := dev.poll(manager.Session(t.Context(), "gentle-otter-482")); err != nil {
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
	dev := devLoop{manager: manager}
	if err := dev.poll(manager.Session(t.Context(), "gentle-otter-482")); !errors.Is(err, errReauthenticationRequired) {
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
	selection := "apps:\n- name: unused\n  path: ./not-checked-out\n  access: {server: {}}\n" +
		"- name: beta\n  remote: {url: https://upstream.example/mcp}\n  access: {server: {}}\n"
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
			name: "command-line sources, file apps and repeated names", file: manifestFileName, manifest: selection,
			apps: []string{"delta=https://upstream.example/mcp", "alpha=https://upstream.example/mcp", "delta", "beta", "--public"},
			want: []string{"alpha", "beta", "delta"},
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
					var input protocol.AppsRequest
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
				}
				installationtest.WriteJSON(w, apiSession(tc.want...))
			})
			h.start(t)
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
	checkout := t.TempDir()
	root := filepath.Join(checkout, "prod")
	document := descriptorDocument("prod")
	document["icons"] = []any{map[string]any{"src": "https://example.com/icon.png", "theme": "light"}}
	document["packages"].([]any)[0].(map[string]any)["environmentVariables"] = []any{
		map[string]any{"name": "TOKEN", "isSecret": true, "isRequired": true},
	}
	writeDescriptor(t, filepath.Join(root, "server.json"), document)
	sourceFiles(t, root, map[string]string{
		"server.js": "export const mcp = 1", "pnpm-lock.yaml": "", ".holon/app.txt": "application file",
		"package.json": `{"private":true,"packageManager":"pnpm@10.34.9","scripts":{"build":"vite build"},"dependencies":{"holon-sh":"^0.0.1"}}`,
	})
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
	var mu sync.Mutex
	var existing bool
	var desired protocol.AppsRequest
	var written []protocol.App
	var sent *protocol.Revision
	var forceConflict, loseResponse bool
	var rolloutFailure string
	revision := 7
	h := newRunHarness(t)
	h.mux.HandleFunc("GET "+protocol.APIPathInfo, func(w http.ResponseWriter, _ *http.Request) {
		installationtest.WriteJSON(w, protocol.Info{
			Installation: protocol.InstallationReference{Origin: h.server.URL, UID: "test-installation"},
			Version:      "test", OAuth: &protocol.OAuthInfo{
				Issuer: h.server.URL + "/", AdminAudience: installationtest.AdminAudience(h.server.URL), CLIClientID: "cli-test",
			},
		})
	})
	builds := serveBuilds(t, h)
	builds.duringFreeze = func() { buildClock.Step(2 * time.Second) }
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
				if loseResponse && installationtest.ToolArguments(r)["revision"] != nil {
					t.Error("deploy waited for a deployment that was already ready")
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
				var input protocol.UpdateSessionRequest
				if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
					t.Error(err)
					return
				}
				sent = input.Desired.Revision
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
	h.start(t)
	runConfig := h.run
	runConfig.Clock = buildClock
	var output, diagnostics bytes.Buffer
	if err := executeCommand(t.Context(), []string{"deploy", "--file", manifestPath}, &output, &diagnostics, runConfig); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	initial := slices.Clone(desired.Apps)
	mu.Unlock()
	steps, err := recipe.Read(os.DirFS(root))
	if err != nil {
		t.Fatal(err)
	}
	builds.mu.Lock()
	build := builds.calls[slices.IndexFunc(builds.calls, func(call buildCall) bool { return call.build.Freeze != nil })]
	wantPlan := runtimecontract.Plan{Install: runtimecontract.Install{Commands: steps.Deploy.Install}, Prepare: steps.Deploy.Build}
	if builds.sessions != 1 || builds.deleted != 1 || !reflect.DeepEqual(build.plan, wantPlan) || string(build.files[".holon/app.txt"]) != "application file" || build.files["generated/holon-sh/index.mjs"] == nil ||
		slices.ContainsFunc(builds.calls, func(call buildCall) bool { return call.build.Freeze == nil && call.plan.Prepare != nil }) {
		t.Fatalf("%d build sessions, deleted %d times, build calls %+v", builds.sessions, builds.deleted, builds.calls)
	}
	builds.mu.Unlock()
	index, manifest, config := assembledImage(t, h.registry, initial[slices.IndexFunc(initial, func(app protocol.App) bool { return app.Name == "prod" })].Image)
	if manifest.Layers[1].Digest != digest.FromString("dependencies of prod") ||
		len(config.RootFS.DiffIDs) != 3 || config.RootFS.DiffIDs[2] != digest.FromString("tar source of prod") {
		t.Fatalf("image layers %+v, diff IDs %v", manifest.Layers, config.RootFS.DiffIDs)
	}
	wantConfig := ocispec.ImageConfig{
		User: "10001:10001", WorkingDir: "/app/source", Entrypoint: []string{"node_modules/.bin/holon-serve", "server.js"},
		Env: []string{"HOLON_RELOAD_DIR=/app/source", "HOME=/tmp", "NODE_ENV=production", "PATH=/usr/local/bin:/usr/bin:/bin", "PYTHONUNBUFFERED=1"},
	}
	labels := config.Config.Labels
	config.Config.Labels = nil
	slices.Sort(config.Config.Env)
	if !reflect.DeepEqual(config.Config, wantConfig) || labels["io.modelcontextprotocol.server.name"] != "io.github.acme/prod" {
		t.Fatalf("image config %+v, labels %v", config.Config, labels)
	}
	if len(index.Manifests) != 2 || index.Manifests[1].ArtifactType != mcpcontract.MediaType || index.Annotations[mcpcontract.DigestAnnotation] == "" {
		t.Fatalf("image index %+v", index)
	}
	repository, err := imageRepository(strings.TrimPrefix(h.registryServer.URL, "https://")+"/holon/upload/prod", "grant-secret", h.registryServer.Client())
	if err != nil {
		t.Fatal(err)
	}
	var releases []string
	h.registry.mu.Lock()
	for name := range h.registry.items {
		if tag, ok := strings.CutPrefix(name, "holon/upload/prod/manifests/"+appartifact.ReleasePrefix); ok {
			releases = append(releases, appartifact.ReleasePrefix+tag)
		}
	}
	h.registry.mu.Unlock()
	if len(releases) != 1 {
		t.Fatalf("releases %v", releases)
	}
	release, err := repository.Resolve(t.Context(), releases[0])
	if err != nil {
		t.Fatal(err)
	}
	record, executable, err := appartifact.Read(t.Context(), repository, release)
	if err != nil || record.AppName != "prod" || !strings.HasSuffix(initial[1].Image, "@"+executable.Digest.String()) ||
		len(record.Runtime.Icons) != 1 || record.Runtime.Icons[0].Source != "https://example.com/icon.png" {
		t.Fatalf("release record %+v: %v", record, err)
	}
	if input := record.Runtime.Env["TOKEN"]; !input.IsSecret || !input.IsRequired {
		t.Fatal("secret declaration was lost")
	}
	if source, err := appartifact.SourceLayer(t.Context(), repository, executable); err != nil || source.Digest != digest.FromString("source of prod") {
		t.Fatalf("source layer %+v: %v", source, err)
	}
	if strings.Contains(diagnostics.String(), "Timing ") ||
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
	git := func(args ...string) string {
		command := exec.Command("git", append([]string{"-C", checkout, "-c", "user.name=test", "-c", "user.email=test@example.com"}, args...)...)
		command.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
		out, err := command.Output()
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return strings.TrimSpace(string(out))
	}
	git("init")
	git("add", ".")
	git("commit", "-m", "deploy")
	head := git("rev-parse", "HEAD")
	sourceFiles(t, checkout, map[string]string{"notes.md": "outside the app"})
	mu.Lock()
	desired.Apps[1].Image = strings.Replace(initial[1].Image, "registry.example/holon/upload/prod", "old-registry.example/prod", 1)
	mu.Unlock()
	if err := executeCommand(t.Context(), []string{"deploy", "--file", manifestPath, "--verbose"}, &output, &diagnostics, runConfig); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diagnostics.String(), "Timing apps prepare contracts: ") ||
		!strings.Contains(diagnostics.String(), "Timing prod build and upload: 2s") {
		t.Fatalf("verbose deployment hid build output or timings: %q", diagnostics.String())
	}
	mu.Lock()
	if desired.Apps[1].Image != initial[1].Image {
		t.Errorf("deployment retained an obsolete repository: %s", desired.Apps[1].Image)
	}
	if !reflect.DeepEqual(sent, &protocol.Revision{Commit: head}) {
		t.Errorf("deploy from a clean checkout sent revision %+v, want commit %s", sent, head)
	}
	forceConflict = true
	mu.Unlock()
	sourceFiles(t, root, map[string]string{"notes.md": "inside the app"})
	err = executeCommand(t.Context(), []string{"deploy", "--file", manifestPath}, &output, nil, runConfig)
	if err == nil || !strings.Contains(err.Error(), "concurrently") {
		t.Fatalf("conditional conflict = %v", err)
	}
	mu.Lock()
	if !reflect.DeepEqual(sent, &protocol.Revision{Commit: head, Dirty: true}) {
		t.Errorf("deploy from a changed checkout sent revision %+v, want commit %s and dirty", sent, head)
	}
	mu.Unlock()
	for _, scenario := range []struct{ name, message string }{
		{"progress deadline", "ProgressDeadlineExceeded: prod: unready replacement"},
		{"superseded", "superseded"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			mu.Lock()
			forceConflict, rolloutFailure = false, scenario.name
			mu.Unlock()
			output.Reset()
			err := executeCommand(t.Context(), []string{"deploy", "--file", manifestPath}, &output, nil, runConfig)
			if err == nil || !strings.Contains(err.Error(), scenario.message) {
				t.Fatalf("rollout result: %v", err)
			}
			if output.Len() != 0 {
				t.Fatalf("failed rollout printed endpoints: %q", output.String())
			}
		})
	}
	updated := "hosted=https://updated.example/mcp"
	for _, scenario := range []string{"disjoint update", "lost accepted response"} {
		t.Run(scenario, func(t *testing.T) {
			mu.Lock()
			desired = protocol.AppsRequest{Apps: slices.Clone(initial)}
			existing, rolloutFailure = true, ""
			forceConflict = scenario == "disjoint update"
			loseResponse = scenario == "lost accepted response"
			mu.Unlock()
			runConfig := h.run
			runConfig.RegistryHTTPClient = installationtest.HTTPClient(func(*http.Request) (*http.Response, error) {
				return nil, errors.New("no access to unrelated application images")
			})
			err := executeCommand(t.Context(), []string{"deploy", updated}, nil, nil, runConfig)
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
		first  = "2026-11-07T10:00:00.1Z INFO alpha started \t\n"
		second = "2026-11-07T10:00:00.2Z INFO alpha multi-line\n  continued\n"
		third  = "2026-11-07T10:00:00.3Z INFO alpha resumed\n"
	)
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		var refreshes int
		var since []string
		mux := http.NewServeMux()
		installationtest.MountDeviceAuth(t, mux, func() string { return "https://apps.example.test/" }, nil, 0, 0,
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
			if strings.Contains(text, "resumed") {
				cancel()
			}
		}}
		input := protocol.SessionLogsRequest{AppRequest: protocol.AppRequest{SessionRequest: protocol.SessionRequest{Session: "alpha"}}, Since: "5m"}
		start := time.Now()
		if err := client.StreamLogs(ctx, input, output); !errors.Is(err, context.Canceled) {
			t.Fatalf("stream error = %v", err)
		}
		// One second after the failed refresh, then one after the dropped stream.
		if elapsed := time.Since(start); elapsed != 2*time.Second || !slices.Equal(since, []string{"5m", "2026-11-07T10:00:00.2Z"}) {
			t.Fatalf("resumed after %v with since = %q", elapsed, since)
		}
		if got := output.String(); strings.Count(got, "started \t\n") != 1 || strings.Count(got, "continued") != 1 || !strings.Contains(got, "resumed") {
			t.Fatalf("output = %q", got)
		}
	})
}
