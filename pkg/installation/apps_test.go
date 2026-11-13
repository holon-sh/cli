package installation

import (
	"bytes"
	"encoding/json"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	testingclock "k8s.io/utils/clock/testing"

	"github.com/holon-sh/cli/pkg/installation/installationtest"
	"github.com/holon-sh/protocol"
)

var columnGap = regexp.MustCompile(` {2,}`)

func TestAppCommandsDefaultToEveryDevelopmentSession(t *testing.T) {
	app := func(name string) protocol.App {
		return protocol.App{
			Name:   name,
			Image:  "registry.example/" + name + "@sha256:" + strings.Repeat("a", 64),
			Access: &protocol.AppAccess{Server: &protocol.AccessRule{}},
		}
	}
	sessions := []protocol.SessionResponse{
		{
			Name: "s-one", Revision: "1", Apps: []protocol.App{app("chat"), app("other")}, ExpiresAt: "2026-11-10T12:08:20Z",
			Endpoints: []protocol.SessionEndpoint{{Name: "chat", URL: "https://s-one.example/chat/mcp"}},
		},
		{Name: "s-two", Revision: "1", Apps: []protocol.App{app("chat"), {
			Name: "draft", Overlay: "sha256:" + strings.Repeat("b", 64), Access: &protocol.AppAccess{Server: &protocol.AccessRule{}},
		}}},
		{Name: "s-three", Revision: "1", Apps: []protocol.App{app("other"), {
			Name: "hf", Remote: &protocol.Remote{URL: "https://huggingface.co/mcp"}, Access: &protocol.AppAccess{Server: &protocol.AccessRule{}},
		}}},
		{Name: "s-empty", Revision: "1", Apps: []protocol.App{}, ExpiresAt: "2026-11-11T11:59:00Z"},
	}
	h := newRunHarness(t)
	var updates []string
	h.control.Handle([]string{toolListSessions}, nil, func(w http.ResponseWriter, _ *http.Request) {
		installationtest.WriteJSON(w, map[string]any{"items": sessions})
	})
	h.control.Handle([]string{protocol.ToolGetSession}, nil, func(w http.ResponseWriter, r *http.Request) {
		var input protocol.SessionRequest
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
			return
		}
		for _, session := range sessions {
			if session.Name == input.Session {
				installationtest.WriteJSON(w, session)
			}
		}
	})
	h.control.Handle([]string{protocol.ToolUpdateSession}, nil, func(w http.ResponseWriter, r *http.Request) {
		var input protocol.UpdateSessionRequestFor[protocol.AppsRequestFor[protocol.App]]
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
			return
		}
		update := input.Session + " remove=" + strings.Join(input.Remove, ",")
		for _, app := range input.Desired.Apps {
			update += " " + app.Name + ":" + accessSummary(app.Access)
		}
		updates = append(updates, update)
		installationtest.WriteJSON(w, protocol.SessionResponse{Name: input.Session, Revision: "2"})
	})
	h.start(t, nil)
	h.run.Clock = testingclock.NewFakeClock(time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC))
	for _, test := range []struct {
		args    []string
		updates []string
		output  []string
		err     string
	}{
		{
			// Every session lists, with its expiry, including one whose apps were all removed.
			args: []string{"app", "list"},
			output: []string{
				"s-one 8m chat https://s-one.example/chat/mcp image registry.example/chat@",
				"s-one 8m other - ",
				"s-two - chat",
				"s-two - draft - source sha256:bbbbbbbbbbbb",
				"s-three - hf - remote huggingface.co public huggingface.co:443",
				"s-empty 23h59m -\n",
			},
		},
		{
			args:    []string{"access", "set", "chat", "oauth", "app:chat:read"},
			updates: []string{"s-one remove= chat:oauth app:chat:read", "s-two remove= chat:oauth app:chat:read"},
			output:  []string{"chat access in dev session s-one: ", "chat access in dev session s-two: "},
		},
		{
			args:    []string{"app", "remove", "chat"},
			updates: []string{"s-one remove=chat", "s-two remove=chat"},
			output:  []string{"Removed chat from dev session s-one.", "Removed chat from dev session s-two."},
		},
		{
			args:   []string{"app", "get", "chat"},
			output: []string{"# dev session s-one", "# dev session s-two"},
		},
		{
			args:    []string{"access", "set", "chat", "public", "--session", "s-two"},
			updates: []string{"s-two remove= chat:public"},
			output:  []string{"chat access in dev session s-two: "},
		},
		{args: []string{"app", "get", "chat", "--session", "s-three"}, err: "app chat is not in dev session s-three"},
		{args: []string{"app", "get", "missing"}, err: "app missing is not in any development session"},
		{args: []string{"access", "delete", "chat/send_message"}, err: "dev session s-one: chat/send_message has no rule"},
	} {
		updates = nil
		var out bytes.Buffer
		err := executeCommand(t.Context(), test.args, &out, nil, h.run)
		if (err == nil) != (test.err == "") || err != nil && !strings.Contains(err.Error(), test.err) {
			t.Fatalf("holon %v: err=%v, want %q", test.args, err, test.err)
		}
		if !slices.Equal(updates, test.updates) {
			t.Fatalf("holon %v: updates=%q, want %q", test.args, updates, test.updates)
		}
		output := columnGap.ReplaceAllString(out.String(), " ")
		for _, want := range test.output {
			if !strings.Contains(output, want) {
				t.Fatalf("holon %v: output=%q, want %q", test.args, output, want)
			}
		}
	}
}
