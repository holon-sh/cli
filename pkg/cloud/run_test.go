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
	"testing"
	"time"

	sharedcli "github.com/holon-sh/cli/pkg/cli"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/oauth2"

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
		`unknown app "missing"`:                    {"deploy", "alpha", "missing", "--project", filepath.Join(root, manifestFileName)},
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

func (h *runHarness) start(t *testing.T) {
	t.Helper()
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
	h.run = commandConfig{Auth: auth}
}

func TestRunDeployLifecycle(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	images, registry := buildRegistry(t)
	source := addTestImage(t, images, "amd64", ocispec.ImageConfig{}, map[string][]byte{"server": []byte("prod")})
	manifestPath := filepath.Join(root, "holon.yaml")
	if err := os.WriteFile(manifestPath, []byte(`apps:
  - name: hosted
    remote: {url: https://mcp.example/mcp}
    headers: {Authorization: {secret: UPSTREAM_TOKEN}, X-Workspace: engineering}
    access: {server: {}}
  - name: prod
    mount: /
    access: {server: {}}
    image: registry.example/source@`+source.Digest.String()+`
`), 0o644); err != nil {
		t.Fatal(err)
	}
	buildClock := testingclock.NewFakeClock(time.Unix(100, 0))
	var mu sync.Mutex
	var existing bool
	var desired protocol.AppsRequestFor[protocol.App]
	var forceConflict, conflicted, loseResponse bool
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
				if forceConflict && !conflicted {
					conflicted = true
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
	h.start(t)
	registryHost := strings.TrimPrefix(registry.URL, "https://")
	runConfig := h.run
	runConfig.Clock = buildClock
	runConfig.RegistryHTTPClient = cloudtest.HTTPClient(func(r *http.Request) (*http.Response, error) {
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
	})
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
	if !strings.Contains(diagnostics.String(), "Timing prod build and upload: ") {
		t.Fatalf("verbose deployment hid timings: %q", diagnostics.String())
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
	} {
		t.Run(scenario.name, func(t *testing.T) {
			manifest := strings.Replace(string(manifest), "https://mcp.example/mcp", "https://updated.example/mcp", 1)
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
			forceConflict, conflicted = scenario.name == "disjoint update", false
			loseResponse = scenario.name == "lost accepted response"
			mu.Unlock()
			runConfig := h.run
			runConfig.RegistryHTTPClient = cloudtest.HTTPClient(func(*http.Request) (*http.Response, error) {
				return nil, errors.New("no access to unrelated application images")
			})
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
				}
			}
		})
	}
}
