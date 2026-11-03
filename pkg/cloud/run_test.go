package cloud

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
	"github.com/holon-sh/cli/pkg/typegen"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/oauth2"

	"k8s.io/apimachinery/pkg/util/wait"
	testingclock "k8s.io/utils/clock/testing"

	"github.com/holon-sh/cli/pkg/protocol"
	"github.com/holon-sh/cli/pkg/cloud/cloudtest"
)

func executeCommand(ctx context.Context, args []string, stdout, stderr io.Writer, config commandConfig) error {
	config.Auth.Env = append([]string{"HOLON_ORIGIN="}, config.Auth.Env...)
	command := sharedcli.NewCommand("dev")
	command.AddCommand(newCommands(config)...)
	command.SetArgs(args)
	command.SetIn(config.Input)
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
		"none of the others can be":                {"config", "set", "--env", "dev", "--session", "s-one", "API_URL", "value"},
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
		for _, tc := range []struct {
			name, access, want string
			oauth              *protocol.OAuthInfo
		}{
			{name: "anonymous build failure", access: `{"server":{}}`},
			{name: "missing identity", access: `{"server":{},"tools":{"read":{"oauth":{}}}}`, want: "requires a configured identity provider"},
			{name: "authenticated build failure", access: `{"server":{"oauth":{"scopes":["app:chat:read"]}}}`, oauth: &protocol.OAuthInfo{}},
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
					info := protocol.Info{Installation: protocol.InstallationReference{Origin: h.server.URL, UID: "test-installation"}}
					if tc.oauth != nil {
						oauth := *tc.oauth
						oauth.Issuer, oauth.AdminAudience, oauth.CLIClientID = h.server.URL+"/", cloudtest.AdminAudience(h.server.URL), "cli-test"
						info.OAuth = &oauth
					}
					cloudtest.WriteJSON(w, info)
				})
				h.control.Handle([]string{protocol.ToolGetSession}, nil, func(w http.ResponseWriter, _ *http.Request) {
					cloudtest.WriteJSON(w, protocol.SessionResponse{
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
	control  *cloudtest.ControlFixtures
	server   *httptest.Server
	baseHost string
	run      commandConfig
}

func newRunHarness(t *testing.T) *runHarness {
	t.Helper()
	h := &runHarness{mux: http.NewServeMux()}
	h.control = cloudtest.NewControlFixtures(t, h.mux)
	cloudtest.MountDeviceAuth(t, h.mux, func() string { return h.server.URL + "/" }, protocol.AdminPlane,
		func(r *http.Request) { cloudtest.AssertOrigin(t, r, h.baseHost) }, 3600, 0,
		func(w http.ResponseWriter, _ *http.Request) {
			cloudtest.WriteJSON(w, map[string]any{"token_type": "Bearer", "access_token": "access-token", "refresh_token": "refresh-token", "expires_in": 3600})
		})
	return h
}

func (h *runHarness) start(t *testing.T, build buildApplication) {
	t.Helper()
	// Development tails completion records; exact-path fixtures take precedence.
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
		HTTPClient: cloudtest.OriginClient(h.server), now: clock.Now, RequestTimeout: time.Second,
	}
	manager := newManager(t, h.server, clock, stateDir)
	if manager.info.OAuth != nil {
		seedAdminCredentials(t, manager, oauth2.Token{
			AccessToken: "access-token", RefreshToken: "refresh-token",
			TokenType: "Bearer", ExpiresIn: 3600, Expiry: clock.Now().Add(time.Hour),
		})
		adminKey := tokenKey(manager.info.OAuth.Issuer, manager.audience(protocol.AdminPlane), protocol.SystemScopes())
		devKey := tokenKey(manager.info.OAuth.Issuer, manager.audience(protocol.DevPlane), protocol.ControlScopes(protocol.DevPlane))
		manager.state.Tokens[devKey] = manager.state.Tokens[adminKey]
		if err := manager.writeState(manager.state); err != nil {
			t.Fatal(err)
		}
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
		RegistryHTTPClient: cloudtest.HTTPClient(func(r *http.Request) (*http.Response, error) {
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

func TestRunDevLifecycle(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for _, name := range []string{"alpha", "beta"} {
		directory := filepath.Join(root, name)
		if err := os.Mkdir(directory, 0o755); err != nil {
			t.Fatal(err)
		}
		sourceFiles(t, directory, map[string]string{"server.js": "export default {}"})
		if err := os.WriteFile(filepath.Join(directory, "package.json"), []byte(sourcePackage), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for name, input := range map[string]map[string]any{
		"alpha": {"name": "API_URL", "isRequired": true},
		"beta":  {"name": "API_KEY", "isRequired": true, "isSecret": true},
	} {
		document := descriptorDocument(name)
		document["packages"].([]any)[0].(map[string]any)["environmentVariables"] = []any{input}
		if name == "alpha" {
			document["_meta"] = map[string]any{publisherMetadata: map[string]any{"sh.holon": map[string]any{
				"requires": map[string]serverReference{"beta": {Name: "io.github.acme/beta", Version: "1.4.2"}},
			}}}
		}
		writeDescriptor(t, filepath.Join(root, name, "server.json"), document)
	}
	sourceFiles(t, filepath.Join(root, "beta"), map[string]string{
		"node_modules/package/index.js": "", ".git/config": "",
	})
	manifestPath := filepath.Join(root, "holon.yaml")
	manifest := `apps:
  - name: alpha
    access: {server: {}}
    path: ./alpha
  - name: beta
    access: {server: {}}
    path: ./beta
`
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	firstBuild := buildImage(t, "first")
	nextBuild := buildImage(t, "next")
	build := func(ctx context.Context, request buildRequest, progress io.Writer) (buildResult, error) {
		if !slices.Equal(request.Platforms, []string{"linux/arm64"}) {
			t.Errorf("session platforms = %v", request.Platforms)
		}
		if _, err := os.Stat(filepath.Join(root, "alpha", "logic.ts")); err == nil {
			return nextBuild(ctx, request, progress)
		}
		return firstBuild(ctx, request, progress)
	}
	var mu sync.Mutex
	var grantRequests, createRequests, deleteRequests int
	var ambiguousCreate bool
	var expireFirstGrant bool
	var deniedTool string
	var retryRequests int
	var waitingReason string
	var sessionBody, servedState protocol.AppsRequestFor[protocol.App]
	var updateBodies []protocol.AppsRequestFor[protocol.App]
	var provisioningSignal chan struct{}
	h := newRunHarness(t)
	h.mux.HandleFunc("GET "+protocol.APIPathInfo, func(w http.ResponseWriter, _ *http.Request) {
		cloudtest.WriteJSON(w, protocol.Info{
			Installation: protocol.InstallationReference{Origin: h.server.URL, UID: "test-installation"},
			Version:      "test", BuildArchitectures: []string{"arm64"}, OAuth: &protocol.OAuthInfo{
				Issuer: h.server.URL + "/", AdminAudience: cloudtest.AdminAudience(h.server.URL), CLIClientID: "cli-test",
			},
		})
	})
	devSession := func() map[string]any {
		response := apiSession(h, "alpha", "beta")
		response["buildArchitectures"] = []string{"arm64"}
		mu.Lock()
		response["apps"] = servedState.Apps
		mu.Unlock()
		return response
	}
	h.control.Handle([]string{protocol.ToolUploadGrant}, nil, func(w http.ResponseWriter, r *http.Request) {
		cloudtest.AssertOrigin(t, r, "dev."+h.baseHost)
		if r.Header.Get(protocol.HeaderAuthorization) != protocol.BearerPrefix+"access-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var upload protocol.UploadRequest
		if err := json.NewDecoder(r.Body).Decode(&upload); err != nil {
			t.Error(err)
			return
		}
		repositories := map[string]string{}
		for _, name := range upload.Apps {
			repositories[name] = "holon/upload/" + name
		}
		mu.Lock()
		grantRequests++
		grant := "grant-secret"
		if expireFirstGrant && grantRequests == 1 {
			grant = "expired-grant"
		}
		mu.Unlock()
		cloudtest.WriteJSON(
			w,
			map[string]any{
				"grant":        grant,
				"registry":     "registry.example",
				"repositories": repositories,
				"expiresAt":    time.Now().Add(time.Hour).Format(time.RFC3339),
			},
		)
	})
	h.control.Handle([]string{protocol.ToolCreateSession}, nil, func(w http.ResponseWriter, r *http.Request) {
		cloudtest.AssertOrigin(t, r, "dev."+h.baseHost)
		if r.Header.Get(protocol.HeaderAuthorization) != protocol.BearerPrefix+"access-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		mu.Lock()
		createRequests++
		decodeErr := json.NewDecoder(r.Body).Decode(&sessionBody)
		servedState = protocol.AppsRequestFor[protocol.App]{Apps: protocol.WritableApps(sessionBody.Apps)}
		ambiguous := ambiguousCreate
		mu.Unlock()
		if decodeErr != nil {
			t.Error(decodeErr)
			return
		}
		if ambiguous {
			connection, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = connection.Close()
			return
		}
		cloudtest.WriteJSON(w, devSession())
	})
	h.control.Handle([]string{protocol.ToolGetSession, protocol.ToolDeleteSession, protocol.ToolUpdateSession, protocol.ToolRenewSession},
		map[string]string{"session": "gentle-otter-482"}, func(w http.ResponseWriter, r *http.Request) {
			cloudtest.AssertOrigin(t, r, "dev."+h.baseHost)
			if cloudtest.ToolName(r) == protocol.ToolDeleteSession {
				mu.Lock()
				deleteRequests++
				mu.Unlock()
				cloudtest.WriteJSON(w, protocol.DeleteSessionResultFor[protocol.SessionResponse]{Deleted: true})
				return
			}
			if cloudtest.ToolName(r) == protocol.ToolUpdateSession {
				var input protocol.UpdateSessionRequestFor[protocol.AppsRequestFor[protocol.App]]
				if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
					t.Error(err)
					return
				}
				if input.ExpectedRevision != "1" {
					http.Error(w, "precondition", http.StatusPreconditionFailed)
					return
				}
				mu.Lock()
				updateBodies = append(updateBodies, input.Desired)
				generation := len(updateBodies) + 1
				servedState = protocol.AppsRequestFor[protocol.App]{Apps: protocol.WritableApps(input.Desired.Apps)}
				mu.Unlock()
				cloudtest.WriteJSON(w, map[string]any{
					"name": "gentle-otter-482", "generation": generation, "buildArchitectures": []string{"arm64"},
					"conditions": []map[string]any{{"type": "Available", "status": "True", "reason": "Serving"}}, "revision": "2",
				})
				return
			}
			mu.Lock()
			reason := waitingReason
			signal := provisioningSignal
			denied := deniedTool == cloudtest.ToolName(r)
			if denied {
				retryRequests++
			}
			mu.Unlock()
			if denied {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			if reason != "" && cloudtest.ToolName(r) == protocol.ToolGetSession {
				cloudtest.WriteJSON(
					w,
					map[string]any{
						"name":               "gentle-otter-482",
						"buildArchitectures": []string{"arm64"},
						"conditions": []map[string]any{{
							"type": "Ready", "status": "False", "reason": reason, "message": "Configure API_URL and API_KEY to start the session.",
						}},
						"revision": "1",
					},
				)
				select {
				case signal <- struct{}{}:
				default:
				}
				return
			}
			cloudtest.WriteJSON(w, devSession())
		})
	// The request stream stays open; with callLogs set it delivers them and ends, as a failed store stream does.
	callLogs, streamRequests := "", 0
	h.mux.HandleFunc("GET /sessions/gentle-otter-482/logs", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		streamRequests++
		content := callLogs
		mu.Unlock()
		if r.URL.Query().Get("event") != "holon.mcp.call" || r.URL.Query().Get("follow") != "true" {
			t.Errorf("request stream query = %s", r.URL.RawQuery)
		}
		w.Header().Set(protocol.HeaderContentType, protocol.MIMETextPlain)
		_, _ = io.WriteString(w, content)
		_ = http.NewResponseController(w).Flush()
		if content == "" {
			<-r.Context().Done()
		}
	})
	h.start(t, build)
	runConfig := func() commandConfig {
		config := h.run
		config.Clock = testingclock.NewFakeClock(time.Now())
		return config
	}
	reset := func() {
		sourceFiles(t, h.run.Auth.ConfigDir, map[string]string{"config.json": fmt.Sprintf(`{"origin":%q}`, h.server.URL)})
		mu.Lock()
		grantRequests, createRequests, deleteRequests = 0, 0, 0
		ambiguousCreate, expireFirstGrant = false, false
		deniedTool = ""
		retryRequests = 0
		waitingReason = ""
		sessionBody, servedState = protocol.AppsRequestFor[protocol.App]{}, protocol.AppsRequestFor[protocol.App]{}
		updateBodies = nil
		provisioningSignal = nil
		callLogs, streamRequests = "", 0
		mu.Unlock()
		_ = os.Remove(filepath.Join(root, "alpha", "logic.ts"))
		_ = os.Remove(filepath.Join(root, "beta", "logic.ts"))
	}
	t.Run("failed rebuild retains pending deployment", func(t *testing.T) {
		reset()
		mu.Lock()
		waitingReason, provisioningSignal = "Progressing", make(chan struct{}, 1)
		signal := provisioningSignal
		mu.Unlock()
		config := runConfig()
		config.builder = func(ctx context.Context, request buildRequest, progress io.Writer) (buildResult, error) {
			if _, err := os.Stat(filepath.Join(root, "alpha", "logic.ts")); err == nil {
				return buildResult{}, errors.New("rebuild failed")
			}
			return firstBuild(ctx, request, progress)
		}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		stdout := &cloudtest.ObservedOutput{OnWrite: func(string) {}}
		stderr := &cloudtest.ObservedOutput{OnWrite: func(text string) {
			if strings.Contains(text, "remote image preparation failed") {
				mu.Lock()
				waitingReason = ""
				mu.Unlock()
			}
		}}
		done := make(chan error, 1)
		go func() {
			done <- executeCommand(ctx, []string{"dev", "--project", manifestPath}, stdout, stderr, config)
		}()
		select {
		case <-signal:
		case err := <-done:
			t.Fatalf("session did not start: %v", err)
		case <-t.Context().Done():
			t.Fatal("session did not start")
		}
		if err := os.WriteFile(filepath.Join(root, "alpha", "logic.ts"), []byte("changed"), 0o644); err != nil {
			t.Fatal(err)
		}
		stepUntil(t, config.Clock.(*testingclock.FakeClock), devDebounce, func() bool {
			return strings.Contains(stdout.String(), "Watching for changes.")
		})
		cancel()
		if err := <-done; err != nil || !strings.Contains(stderr.String(), "alpha ready") {
			t.Fatalf("previous deployment lost after rebuild failure: %v, %s", err, stderr.String())
		}
	})
	t.Run("expired grant reuses builds and cleans up the session", func(t *testing.T) {
		reset()
		mu.Lock()
		expireFirstGrant = true
		mu.Unlock()
		config := runConfig()
		var builds atomic.Int32
		config.builder = func(ctx context.Context, request buildRequest, progress io.Writer) (buildResult, error) {
			builds.Add(1)
			return firstBuild(ctx, request, progress)
		}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		stdout := &cloudtest.ObservedOutput{OnWrite: func(value string) {
			if strings.Contains(value, "Watching for changes.") {
				cancel()
			}
		}}
		var stderr bytes.Buffer
		if err := executeCommand(ctx, []string{"dev", "--project", manifestPath}, stdout, &stderr, config); err != nil {
			t.Fatalf("run: %v stdout=%q stderr=%q", err, stdout.String(), stderr.String())
		}
		mu.Lock()
		grants, creates, deletes := grantRequests, createRequests, deleteRequests
		body := sessionBody
		mu.Unlock()
		if grants != 2 || builds.Load() != 2 {
			t.Fatalf("grants=%d, builds=%d", grants, builds.Load())
		}
		if creates != 1 || deletes != 1 {
			t.Fatalf("session lifecycle creates=%d deletes=%d", creates, deletes)
		}

		if len(body.Apps) != 2 || !strings.HasPrefix(body.Apps[0].Image, "registry.example/holon/upload/alpha@sha256:") ||
			body.Apps[0].Env["API_URL"].Kind() != "config" || body.Apps[1].Env["API_KEY"].Kind() != "secret" ||
			!slices.Equal(body.Apps[0].Requires, []string{"beta"}) {
			t.Fatalf("session images = %#v", body.Apps)
		}
		origin := "https://gentle-otter-482." + h.baseHost
		for _, endpoint := range []string{"/mcp", "/alpha/", "/mcp/alpha", "/beta/", "/mcp/beta"} {
			if !slices.Contains(strings.Fields(stdout.String()), origin+endpoint) {
				t.Errorf("missing endpoint %s in %q", endpoint, stdout.String())
			}
		}
		if strings.Contains(stderr.String(), "fixture build output") ||
			!strings.Contains(stderr.String(), "alpha building") || !strings.Contains(stderr.String(), "alpha ready") ||
			strings.ContainsAny(stdout.String()+stderr.String(), "\x1b\r") {
			t.Fatalf("compact build output: stdout=%q stderr=%q", stdout.String(), stderr.String())
		}
		if strings.Contains(stdout.String()+stderr.String(), "grant-secret") {
			t.Fatalf("output leaked credentials: stdout=%q stderr=%q", stdout.String(), stderr.String())
		}
	})
	descriptorPath := filepath.Join(root, "alpha", "server.json")
	descriptor, err := os.ReadFile(descriptorPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, edit := range []struct {
		name, file, contents, progress, nextFile string
		apps                                     []string
	}{
		{
			name: "source edits retain the session target after selection changes", file: filepath.Join(root, "alpha", "logic.ts"),
			contents: "export const revision = 2\n", progress: "remote update requested (generation ",
			nextFile: filepath.Join(root, "beta", "logic.ts"), apps: []string{"alpha", "beta"},
		},
		{
			name: "OAuth edit", file: manifestPath,
			contents: strings.Replace(manifest, "name: alpha", "name: alpha\n    oauth: {clientID: registered}", 1), progress: "restart holon dev",
		},
		{
			name: "port edit", file: descriptorPath, contents: strings.Replace(string(descriptor), "localhost:3000", "localhost:8080", 1),
			progress: "restart holon dev",
		},
	} {
		t.Run(edit.name, func(t *testing.T) {
			reset()
			t.Cleanup(func() {
				_ = os.WriteFile(manifestPath, []byte(manifest), 0o644)
				_ = os.WriteFile(descriptorPath, descriptor, 0o600)
			})
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			fc := testingclock.NewFakeClock(time.Now())
			config := runConfig()
			config.Clock = fc
			write := func(file string) {
				if err := os.WriteFile(file, []byte(edit.contents), 0o644); err != nil {
					t.Error(err)
				}
			}
			var once sync.Once
			stdout := &cloudtest.ObservedOutput{OnWrite: func(value string) {
				if strings.Contains(value, "Watching for changes.") {
					once.Do(func() {
						if len(edit.apps) != 0 {
							selection := filepath.Join(config.Auth.ConfigDir, "config.json")
							if err := os.WriteFile(selection, []byte(`{"origin":"https://other.example"}`), 0o600); err != nil {
								t.Error(err)
							}
							for _, file := range []string{"node_modules/package/index.js", ".git/config"} {
								if err := os.WriteFile(filepath.Join(root, "beta", file), []byte("changed"), 0o644); err != nil {
									t.Error(err)
								}
							}
						}
						write(edit.file)
					})
				}
			}}
			var observed atomic.Bool
			var next sync.Once
			stderr := &cloudtest.ObservedOutput{OnWrite: func(value string) {
				count := strings.Count(value, edit.progress)
				if count == 1 && edit.nextFile != "" {
					next.Do(func() { write(edit.nextFile) })
				}
				if count >= max(1, len(edit.apps)) {
					observed.Store(true)
				}
			}}
			done := make(chan error, 1)
			go func() {
				done <- executeCommand(ctx, []string{"dev", "--project", manifestPath}, stdout, stderr, config)
			}()
			stepUntil(t, fc, devDebounce, observed.Load)
			cancel()
			if err := <-done; err != nil {
				t.Fatalf("run: %v stderr=%q", err, stderr.String())
			}
			mu.Lock()
			deletes := deleteRequests
			bodies := slices.Clone(updateBodies)
			mu.Unlock()
			if len(bodies) != len(edit.apps) || deletes != 1 {
				t.Fatalf("updates=%v delete=%d stderr=%q", bodies, deletes, stderr.String())
			}
			for i, body := range bodies {
				if len(body.Apps) != 2 || !slices.ContainsFunc(body.Apps, func(app protocol.App) bool { return app.Name == edit.apps[i] }) {
					t.Fatalf("image update = %#v", body)
				}
			}
		})
	}
	t.Run("cancel while waiting for configuration", func(t *testing.T) {
		reset()
		mu.Lock()
		waitingReason = "WaitingForConfiguration"
		mu.Unlock()
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		stderr := &cloudtest.ObservedOutput{OnWrite: func(text string) {
			if strings.Contains(text, "Configure API_URL and API_KEY") {
				cancel()
			}
		}}
		var stdout bytes.Buffer
		if err := executeCommand(ctx, []string{"dev", "--project", manifestPath}, &stdout, stderr, runConfig()); err != nil {
			t.Fatalf("cancellation = %v", err)
		}
		if strings.Count(stderr.String(), "Configure API_URL and API_KEY to start the session.") != 1 {
			t.Fatalf("configuration prompt = %q", stderr.String())
		}
		if stdout.String() != "Session  gentle-otter-482\n" {
			t.Fatalf("output before ready = %q", stdout.String())
		}
		mu.Lock()
		creates, deletes := createRequests, deleteRequests
		mu.Unlock()
		if creates != 1 || deletes != 1 {
			t.Fatalf("cancellation cleanup: creates=%d deletes=%d", creates, deletes)
		}
	})
	t.Run("session status and renewal recover from retries", func(t *testing.T) {
		reset()
		config := runConfig()
		fc := config.Clock.(*testingclock.FakeClock)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		stdout := &cloudtest.ObservedOutput{OnWrite: func(string) {}}
		stderr := &cloudtest.ObservedOutput{OnWrite: func(string) {}}
		done := make(chan error, 1)
		go func() {
			done <- executeCommand(ctx, []string{"dev", "--project", manifestPath}, stdout, stderr, config)
		}()
		stepUntil(t, fc, workflowPoll, func() bool {
			return strings.Contains(stdout.String(), "Watching for changes.")
		})
		for _, operation := range []struct{ name, tool string }{
			{"session status", protocol.ToolGetSession},
			{"session renewal", protocol.ToolRenewSession},
		} {
			mu.Lock()
			deniedTool = operation.tool
			retryRequests = 0
			mu.Unlock()
			stepUntil(t, fc, renewalInterval, func() bool {
				mu.Lock()
				requests := retryRequests
				mu.Unlock()
				return requests >= 3 && strings.Contains(stderr.String(), operation.name+" retrying:")
			})
			if count := strings.Count(stderr.String(), operation.name+" retrying:"); count != 1 {
				t.Fatalf("repeated failure printed %d times: %s", count, stderr.String())
			}
			recoveries := strings.Count(stderr.String(), operation.name+" ok")
			mu.Lock()
			deniedTool = ""
			mu.Unlock()
			stepUntil(t, fc, renewalInterval, func() bool {
				return strings.Count(stderr.String(), operation.name+" ok") > recoveries
			})
		}
		cancel()
		if err := <-done; err != nil {
			t.Fatalf("retry = %v stderr=%q", err, stderr.String())
		}
		mu.Lock()
		creates, deletes := createRequests, deleteRequests
		mu.Unlock()
		if creates != 1 || deletes != 1 {
			t.Fatalf("retry cleanup: creates=%d deletes=%d", creates, deletes)
		}
	})
	t.Run("request stream failure does not stop development", func(t *testing.T) {
		reset()
		mu.Lock()
		callLogs = "2030-01-01T12:00:01Z INFO alpha tools/call echo ok 1ms trace_id=aaaa\n"
		mu.Unlock()
		config := runConfig()
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		stdout := &cloudtest.ObservedOutput{OnWrite: func(string) {}}
		stderr := &cloudtest.ObservedOutput{OnWrite: func(string) {}}
		done := make(chan error, 1)
		go func() {
			done <- executeCommand(ctx, []string{"dev", "--project", manifestPath}, stdout, stderr, config)
		}()
		stepUntil(t, config.Clock.(*testingclock.FakeClock), workflowPoll, func() bool {
			return strings.Contains(stdout.String(), "trace_id=aaaa") && strings.Contains(stdout.String(), "Ready") &&
				strings.Contains(stderr.String(), "request stream stopped")
		})
		cancel()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		count := streamRequests
		mu.Unlock()
		if strings.Count(stdout.String(), "aaaa") != 1 || count != 1 {
			t.Fatalf("request stream =\n%s", stdout.String())
		}
	})
	t.Run("ambiguous creation is not retried or deleted", func(t *testing.T) {
		reset()
		mu.Lock()
		ambiguousCreate = true
		mu.Unlock()
		var stdout, stderr bytes.Buffer
		err := executeCommand(t.Context(), []string{"dev", "--project", manifestPath}, &stdout, &stderr, runConfig())
		if err == nil || !strings.Contains(err.Error(), "outcome is unknown") ||
			!strings.Contains(err.Error(), "https://dev."+h.baseHost+" before retrying") {
			t.Fatalf("ambiguous create error = %v", err)
		}
		mu.Lock()
		creates, deletes := createRequests, deleteRequests
		mu.Unlock()
		if creates != 1 || deletes != 0 {
			t.Fatalf("ambiguous creation was lost or deleted: creates=%d deletes=%d", creates, deletes)
		}
	})
}

func TestDevDeploymentStatusTransitions(t *testing.T) {
	h := newRunHarness(t)
	h.mux.HandleFunc("GET "+protocol.APIPathInfo, func(w http.ResponseWriter, _ *http.Request) {
		cloudtest.WriteJSON(w, protocol.Info{Installation: protocol.InstallationReference{Origin: h.server.URL, UID: "test-installation"}})
	})
	responses := make(chan protocol.SessionResponse, 1)
	h.control.Handle([]string{protocol.ToolGetSession}, nil, func(w http.ResponseWriter, _ *http.Request) {
		cloudtest.WriteJSON(w, <-responses)
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
	response := cloudtest.SessionResponse("gentle-otter-482", "")
	endpoints := make([]protocol.SessionEndpoint, 0, len(names))
	for _, name := range names {
		endpoints = append(endpoints, protocol.SessionEndpoint{Name: name, URL: "https://gentle-otter-482." + h.baseHost + protocol.AppPath(name)})
	}
	response["endpoints"] = endpoints
	return response
}

func TestRunDevImportsImageEditsAndRecoversFromFailedImport(t *testing.T) {
	fixture := newImageRegistry()
	first := addTestImage(t, fixture, "amd64", ocispec.ImageConfig{}, nil).Digest.String()
	second := addTestImage(t, fixture, "arm64", ocispec.ImageConfig{}, nil).Digest.String()
	registryServer := httptest.NewTLSServer(fixture)
	defer registryServer.Close()
	registryHost := strings.TrimPrefix(registryServer.URL, "https://")
	root := t.TempDir()
	manifestPath := filepath.Join(root, manifestFileName)
	editPath := manifestPath
	manifest := func(imageDigest string) string {
		return "apps:\n  - name: alpha\n    access: {server: {}}\n    port: 9090\n" +
			"    image: " + registryHost + "/source@" + imageDigest + "\n" +
			"  - name: beta\n    remote: {url: https://upstream.example/mcp}\n    access: {server: {}}\n" +
			"  - name: unused\n    path: ./not-checked-out\n    access: {server: {}}\n"
	}
	if err := os.WriteFile(editPath, []byte(manifest(first)), 0o644); err != nil {
		t.Fatal(err)
	}
	h := newRunHarness(t)
	var creates, patches, deletes atomic.Int32
	var stateMu sync.Mutex
	var state protocol.AppsRequestFor[protocol.App]
	currentResponse := func() map[string]any {
		stateMu.Lock()
		defer stateMu.Unlock()
		result := apiSession(h, "alpha", "beta")
		result["apps"] = state.Apps
		return result
	}
	h.control.Handle([]string{protocol.ToolUploadGrant}, nil, func(w http.ResponseWriter, r *http.Request) {
		cloudtest.WriteJSON(w, protocol.UploadGrant{
			Registry: registryHost, Grant: "grant-secret", Repositories: map[string]string{"alpha": "holon/upload/echo", "beta": "unused"},
			ExpiresAt: time.Now().Add(time.Hour).Format(time.RFC3339),
		})
	})
	h.control.Handle([]string{protocol.ToolCreateSession}, nil, func(w http.ResponseWriter, r *http.Request) {
		creates.Add(1)
		var created protocol.AppsRequestFor[protocol.App]
		err := json.NewDecoder(r.Body).Decode(&created)
		stateMu.Lock()
		state = created
		stateMu.Unlock()
		alpha := slices.IndexFunc(created.Apps, func(app protocol.App) bool { return app.Name == "alpha" })
		beta := slices.ContainsFunc(created.Apps, func(app protocol.App) bool { return app.Name == "beta" })
		if err != nil || len(created.Apps) != 2 || alpha < 0 || !beta || created.Apps[alpha].Port != 9090 ||
			created.Apps[alpha].Image != registryHost+"/holon/upload/echo@"+first {
			t.Errorf("session creation: %#v (%v)", created, err)
		}
		cloudtest.WriteJSON(w, currentResponse())
	})
	h.control.Handle([]string{protocol.ToolGetSession, protocol.ToolDeleteSession, protocol.ToolUpdateSession},
		map[string]string{"session": "gentle-otter-482"}, func(w http.ResponseWriter, r *http.Request) {
			switch cloudtest.ToolName(r) {
			case protocol.ToolGetSession:
				cloudtest.WriteJSON(w, currentResponse())
			case protocol.ToolUpdateSession:
				patches.Add(1)
				var input protocol.UpdateSessionRequestFor[protocol.AppsRequestFor[protocol.App]]
				err := json.NewDecoder(r.Body).Decode(&input)
				if input.ExpectedRevision != "1" {
					http.Error(w, "precondition", http.StatusPreconditionFailed)
					return
				}
				patched := input.Desired
				if err != nil || len(patched.Apps) != 2 || patched.Apps[0].Image != registryHost+"/holon/upload/echo@"+second {
					t.Errorf("session update: %#v (%v)", patched, err)
				}
				cloudtest.WriteJSON(w, map[string]any{"name": "gentle-otter-482", "generation": 2, "revision": "2"})
			case protocol.ToolDeleteSession:
				deletes.Add(1)
				cloudtest.WriteJSON(w, protocol.DeleteSessionResultFor[protocol.SessionResponse]{Deleted: true})
			default:
				w.WriteHeader(http.StatusMethodNotAllowed)
			}
		})
	h.control.Handle([]string{protocol.ToolRenewSession}, nil, func(w http.ResponseWriter, r *http.Request) {
		cloudtest.WriteJSON(w, currentResponse())
	})
	h.start(t, nil)
	h.run.Env = append(h.run.Env, "DOCKER_CONFIG="+t.TempDir())
	fc := testingclock.NewFakeClock(time.Now())
	h.run.Clock = fc
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var started, failed sync.Once
	stdout := &cloudtest.ObservedOutput{OnWrite: func(text string) {
		if strings.Contains(text, "/mcp/alpha") {
			started.Do(func() {
				if err := os.WriteFile(editPath, []byte(manifest("sha256:"+strings.Repeat("f", 64))), 0o644); err != nil {
					t.Error(err)
				}
			})
		}
	}}
	var updated atomic.Bool
	stderr := &cloudtest.ObservedOutput{OnWrite: func(text string) {
		if strings.Contains(text, "remote image preparation failed") {
			failed.Do(func() {
				if err := os.WriteFile(editPath, []byte(manifest(second)), 0o644); err != nil {
					t.Error(err)
				}
			})
		}
		if strings.Contains(text, "remote update requested") {
			updated.Store(true)
		}
	}}
	done := make(chan error, 1)
	go func() {
		done <- executeCommand(ctx, []string{"dev", "alpha", "beta", "--project", manifestPath}, stdout, stderr, h.run)
	}()
	stepUntil(t, fc, devDebounce, updated.Load)
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("image dev run: %v, stdout=%q stderr=%q", err, stdout.String(), stderr.String())
	}
	if creates.Load() != 1 || patches.Load() != 1 || deletes.Load() != 1 {
		t.Fatalf("creates=%d patches=%d deletes=%d stderr=%q", creates.Load(), patches.Load(), deletes.Load(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "remote image preparation failed") || !strings.Contains(stderr.String(), "remote update requested") {
		t.Fatalf("failure and recovery not reported: %q", stderr.String())
	}
}

func TestRunDevRemoteWithoutBuildContext(t *testing.T) {
	for _, tc := range []struct {
		name, oauth    string
		manifest       string
		extraFiles     map[string]string
		apps, wantApps []string
		want           *protocol.OAuthClient
	}{
		{name: "automatic", extraFiles: map[string]string{"holon.yml": "invalid manifest"}},
		{name: "yml fallback", manifest: "holon.yml"},
		{name: "selected app and dependencies", apps: []string{"alpha"}, wantApps: []string{"alpha", "beta", "gamma"}},
		{name: "multiple apps and repeated names", apps: []string{"delta", "alpha", "delta"}, wantApps: []string{"alpha", "beta", "delta", "gamma"}},
		{name: "public", oauth: "  oauth: {clientID: registered}\n", want: &protocol.OAuthClient{ClientID: "registered"}},
		{
			name: "confidential", oauth: "  oauth: {clientID: '{CLIENT_ID}', clientSecret: {secret: CLIENT_SECRET}}\n",
			want: &protocol.OAuthClient{ClientID: "{CLIENT_ID}", ClientSecret: "CLIENT_SECRET"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			args := []string{"dev"}
			wantApps := []string{"alpha"}
			manifest := "apps:\n- name: alpha\n" +
				"  remote: {url: https://upstream.example/mcp}\n  access: {server: {}}\n" + tc.oauth
			if len(tc.apps) > 0 {
				args = append([]string{"dev", "--project", filepath.Join(root, manifestFileName)}, tc.apps...)
				wantApps = tc.wantApps
				manifest = "apps:\n" +
					"- name: unused\n  path: ./not-checked-out\n  access: {server: {}}\n" +
					"- name: delta\n  remote: {url: https://upstream.example/mcp}\n  access: {server: {}}\n"
				var dependency *registrySource
				var dependencyName string
				var declarations []string
				for _, name := range []string{"gamma", "beta", "alpha"} {
					document := descriptorDocument(name)
					delete(document, "packages")
					document["remotes"] = []any{map[string]any{"type": "streamable-http", "url": "https://upstream.example/mcp"}}
					if dependency != nil {
						document["_meta"] = map[string]any{publisherMetadata: map[string]any{"sh.holon": map[string]any{
							"requires": map[string]serverReference{dependencyName: dependency.RegistryRef},
						}}}
					}
					dependency, dependencyName = descriptorRegistry(t, document), name
					data, _ := json.Marshal(dependency)
					declarations = append(declarations, fmt.Sprintf("- name: %s\n  registry: %s\n  access: {server: {}}\n", name, data))
				}
				manifest += strings.Join(declarations, "")
			}
			file := tc.manifest
			if file == "" {
				file = manifestFileName
			}
			sourceFiles(t, root, tc.extraFiles)
			if err := os.WriteFile(filepath.Join(root, file), []byte(manifest), 0o600); err != nil {
				t.Fatal(err)
			}
			h := newRunHarness(t)
			h.control.Handle([]string{protocol.ToolUploadGrant}, nil, func(w http.ResponseWriter, r *http.Request) {
				repositories := map[string]string{}
				for _, name := range wantApps {
					repositories[name] = "unused"
				}
				cloudtest.WriteJSON(w, protocol.UploadGrant{
					Registry: "registry.example", Grant: "unused", Repositories: repositories,
					ExpiresAt: time.Now().Add(time.Hour).Format(time.RFC3339),
				})
			})
			h.control.Handle([]string{protocol.ToolCreateSession}, nil, func(w http.ResponseWriter, r *http.Request) {
				var input protocol.AppsRequestFor[protocol.App]
				if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
					t.Error(err)
					return
				}
				var names []string
				for _, app := range input.Apps {
					names = append(names, app.Name)
				}
				slices.Sort(names)
				if !slices.Equal(names, wantApps) ||
					input.Apps[0].Remote == nil || input.Apps[0].Remote.URL != "https://upstream.example/mcp" ||
					input.Apps[0].Image != "" || input.Apps[0].Port != 0 {
					t.Errorf("unexpected remote session: %#v", input)
					return
				}
				app := input.Apps[0]
				if (app.OAuth == nil) != (tc.want == nil) || app.OAuth != nil && *app.OAuth != *tc.want {
					t.Error("OAuth declaration was not preserved in the session request")
				}
				for name := range app.OAuth.Inputs() {
					if _, injected := app.Env[name]; injected {
						t.Error("OAuth input injected into the application environment")
					}
				}
				cloudtest.WriteJSON(w, apiSession(h, wantApps...))
			})
			var deleted atomic.Bool
			h.control.Handle([]string{protocol.ToolGetSession, protocol.ToolDeleteSession}, map[string]string{"session": "gentle-otter-482"},
				func(w http.ResponseWriter, r *http.Request) {
					if cloudtest.ToolName(r) == protocol.ToolDeleteSession {
						deleted.Store(true)
						cloudtest.WriteJSON(w, protocol.DeleteSessionResultFor[protocol.SessionResponse]{Deleted: true})
						return
					}
					cloudtest.WriteJSON(w, apiSession(h, wantApps...))
				})
			h.start(t, nil)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			var started atomic.Bool
			output := &cloudtest.ObservedOutput{OnWrite: func(text string) {
				if strings.Contains(text, "/mcp/alpha") {
					started.Store(true)
					cancel()
				}
			}}
			err := executeCommand(ctx, args, output, io.Discard, h.run)
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if !started.Load() || !deleted.Load() {
				t.Fatal("remote development did not start and clean up")
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
    headers: {Authorization: {secret: UPSTREAM_TOKEN}, X-Workspace: engineering}
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
	var forceConflict, loseResponse bool
	var rolloutFailure string
	revision := 7
	h := newRunHarness(t)
	h.mux.HandleFunc("GET "+protocol.APIPathInfo, func(w http.ResponseWriter, _ *http.Request) {
		cloudtest.WriteJSON(w, protocol.Info{
			Installation: protocol.InstallationReference{Origin: h.server.URL, UID: "test-installation"},
			Version:      "test", BuildArchitectures: []string{"amd64"}, OAuth: &protocol.OAuthInfo{
				Issuer: h.server.URL + "/", AdminAudience: cloudtest.AdminAudience(h.server.URL), CLIClientID: "cli-test",
			},
		})
	})
	h.control.Handle([]string{protocol.ToolUploadGrant}, nil, func(w http.ResponseWriter, r *http.Request) {
		cloudtest.AssertOrigin(t, r, "admin."+h.baseHost)
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
		cloudtest.WriteJSON(
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
			cloudtest.AssertOrigin(t, r, "admin."+h.baseHost)
			mu.Lock()
			defer mu.Unlock()
			switch cloudtest.ToolName(r) {
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
				cloudtest.WriteJSON(w, protocol.SessionResponse{
					Name: "production", UID: "production-uid", Apps: desired.Apps, Generation: generation, Revision: strconv.Itoa(revision),
					Endpoints: []protocol.SessionEndpoint{{Name: "prod", URL: "https://apps.example/mcp/prod"}}, Conditions: conditions,
				})
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
				cloudtest.WriteJSON(w, protocol.SessionResponse{
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
	if len(initial) != 2 || initial[0].Name != "hosted" || initial[0].Remote == nil ||
		initial[0].Remote.URL != "https://mcp.example/mcp" || !initial[0].Env["UPSTREAM_TOKEN"].IsSecret ||
		initial[0].Port != 0 || initial[0].Image != "" || initial[1].Image == "" {
		t.Fatalf("mixed source and remote deployment lost configuration: %#v", initial)
	}
	remote := initial[0]
	if remote.Remote.Headers["X-Workspace"] != "engineering" || remote.Remote.Headers["Authorization"] != "{UPSTREAM_TOKEN}" {
		t.Errorf("inline remote lost its headers: %v", remote.Remote.Headers)
	}
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
	} {
		t.Run(scenario.name, func(t *testing.T) {
			manifest := strings.Replace(string(manifest), "https://mcp.example/mcp", "https://updated.example/mcp", 1)
			if scenario.name == "retain dependency without checkout" {
				doc := descriptorDocument("hosted")
				delete(doc, "packages")
				doc["remotes"] = []any{map[string]any{"type": "streamable-http", "url": "https://updated.example/mcp"}}
				doc["_meta"] = map[string]any{publisherMetadata: map[string]any{"sh.holon": map[string]any{
					"requires": map[string]serverReference{"prod": {Name: "io.github.acme/prod", Version: "1.4.2"}},
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
			mu.Unlock()
			runConfig := h.run
			if scenario.name != "retain dependency without checkout" {
				runConfig.RegistryHTTPClient = cloudtest.HTTPClient(func(*http.Request) (*http.Response, error) {
					return nil, errors.New("no access to unrelated application images")
				})
			}
			if err := executeCommand(t.Context(), []string{"deploy", "hosted", "--project", manifestPath}, nil, nil, runConfig); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			defer mu.Unlock()
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
