package installation

import (
	"bytes"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/holon-sh/cli/pkg/installation/installationtest"
	"github.com/holon-sh/protocol"
)

func TestAppCommandsDefaultToEveryDevelopmentSession(t *testing.T) {
	app := func(name string) protocol.App {
		return protocol.App{
			Name:   name,
			Image:  "registry.example/" + name + "@sha256:" + strings.Repeat("a", 64),
			Access: &protocol.AppAccess{Server: &protocol.AccessRule{}},
		}
	}
	sessions := []protocol.SessionResponse{
		{Name: "s-one", Revision: "1", Apps: []protocol.App{app("chat"), app("other")}},
		{Name: "s-two", Revision: "1", Apps: []protocol.App{app("chat")}},
		{Name: "s-three", Revision: "1", Apps: []protocol.App{app("other")}},
	}
	h := newRunHarness(t)
	var updates []string
	h.control.Handle([]string{toolListSessions}, nil, func(w http.ResponseWriter, _ *http.Request) {
		installationtest.WriteJSON(w, map[string]any{"items": sessions})
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
	for _, test := range []struct {
		args    []string
		updates []string
		output  []string
		err     string
	}{
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
		for _, want := range test.output {
			if !strings.Contains(out.String(), want) {
				t.Fatalf("holon %v: output=%q, want %q", test.args, out.String(), want)
			}
		}
	}
}
