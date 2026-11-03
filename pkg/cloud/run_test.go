package cloud

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	sharedcli "github.com/holon-sh/cli/pkg/cli"
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
	command.SetOut(cmp.Or(stdout, io.Discard))
	command.SetErr(cmp.Or(stderr, io.Discard))
	return command.ExecuteContext(ctx)
}

func TestRunRejectsInvalidInputs(t *testing.T) {
	t.Parallel()
	for want, args := range map[string][]string{
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
	sourceFiles(t, stateDir, map[string]string{"config.json": fmt.Sprintf(`{"origin":%q}`, h.server.URL)})
	h.run = commandConfig{Auth: auth}
}
