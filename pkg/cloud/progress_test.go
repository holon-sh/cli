package cloud

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"testing"
	"testing/synctest"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/fsnotify/fsnotify"
	"k8s.io/utils/clock"
)

func TestDevWatcherReportsWithoutChangeConsumer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var stream bytes.Buffer
		output, _, _ := newProgressOutput(io.Discard, &stream, false, clock.RealClock{})
		events := make(chan fsnotify.Event)
		watcher := devWatcher{
			watcher: &fsnotify.Watcher{Events: events, Errors: make(chan error)},
			output:  output, done: make(chan struct{}),
		}
		go watcher.run(make(chan devChange))
		// A build can leave changes unread. Diagnostics must still arrive, without
		// buffering or dropping a later error behind the first one.
		for _, err := range []error{fsnotify.ErrEventOverflow, io.ErrUnexpectedEOF} {
			watcher.watcher.Errors <- err
		}
		close(events)
		<-watcher.done
		want := "file watcher continuing after error: " + fsnotify.ErrEventOverflow.Error() + "\n" +
			"file watcher continuing after error: unexpected EOF\n"
		if stream.String() != want {
			t.Fatalf("watcher diagnostics = %q, want %q", stream.String(), want)
		}
	})
}

func TestProgressTransitions(t *testing.T) {
	for _, tc := range []struct {
		name, message string
		state         tea.ProgramState
		err           error
	}{
		{name: "terminal canceled", err: context.Canceled, state: "idle", message: "Canceled"},
		{name: "terminal succeeded", state: "done", message: "Finished"},
		{name: "terminal failed", err: errors.New("command failed"), state: "error", message: "command failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stream bytes.Buffer
			output, _, _ := newProgressOutput(io.Discard, &stream, false, clock.RealClock{})
			output.program = tea.NewProgram(newProgressModel(), tea.WithOutput(&stream), tea.WithInput(nil),
				tea.WithoutSignalHandler(), tea.WithEnvironment([]string{"TERM=xterm-256color"}), tea.WithWindowSize(120, 40))
			output.renderer.Go(func() error {
				_, err := output.program.Run()
				return err
			})
			for range 2 {
				output.status("alpha", "building", progressWorking)
				output.retry("session status", errors.New("offline"))
				output.retry("session renewal", errReauthenticationRequired)
				output.status("", "Watching for changes", progressIdle)
			}
			for range 2 {
				output.retry("session status", nil)
				output.retry("session renewal", nil)
			}
			output.retry("session status", errors.New("offline"))
			output.retry("session status", errors.New("different failure"))
			output.retry("session renewal", errReauthenticationRequired)
			output.retry("session status", context.Canceled)
			output.status("beta", "ready", progressDone)
			if err := output.finish(tc.err, "Finished"); err != nil {
				t.Fatal(err)
			}
			visible := strings.Join(strings.Fields(ansi.Strip(stream.String())), " ")
			for _, want := range []string{"alpha " + tc.message, "beta ready", "session renewal " + tc.message, "session status " + tc.message} {
				if !strings.Contains(visible, want) {
					t.Fatalf("final terminal status = %q, missing %q", visible, want)
				}
			}

			// Compare the emitted records with the library's encoding of the contract.
			// Normalize opaque child IDs by first appearance: identities must remain
			// distinct and stable, but their hashing algorithm is not a contract.
			reports := strings.Join(regexp.MustCompile("\x1b]7501;[^\x07]*\x07").FindAllString(stream.String(), -1), "")
			ids := map[string]string{}
			reports = regexp.MustCompile(`id=holon/items/[^:\x07]+`).ReplaceAllStringFunc(reports, func(id string) string {
				ids[id] = cmp.Or(ids[id], fmt.Sprintf("id=holon/items/%d", len(ids)))
				return ids[id]
			})
			var want strings.Builder
			for _, status := range []tea.ProgramStatus{
				{ID: "holon/items/0", Title: "alpha", Message: "building", State: tea.ProgramStateWorking},
				{ID: "holon/items/1", Title: "session status", Message: "retrying: offline", State: tea.ProgramStateWorking},
				{
					ID: "holon/items/2", Title: "session renewal", Message: "reauthentication required; rerun the command",
					State: tea.ProgramStateBlocked, Kind: tea.ProgramStatusKindAuth,
				},
				{ID: "holon", Message: "Watching for changes", State: tea.ProgramStateIdle},
				{ID: "holon/items/1", Title: "session status", Message: "ok", State: tea.ProgramStateDone},
				{ID: "holon/items/2", Title: "session renewal", Message: "ok", State: tea.ProgramStateDone},
				{ID: "holon/items/1", Title: "session status", Message: "retrying: offline", State: tea.ProgramStateWorking},
				{ID: "holon/items/1", Title: "session status", Message: "retrying: different failure", State: tea.ProgramStateWorking},
				{
					ID: "holon/items/2", Title: "session renewal", Message: "reauthentication required; rerun the command",
					State: tea.ProgramStateBlocked, Kind: tea.ProgramStatusKindAuth,
				},
				{ID: "holon/items/3", Title: "beta", Message: "ready", State: tea.ProgramStateDone},
			} {
				status.App = "holon"
				want.WriteString(ansi.SetProgramStatus(status))
			}
			want.WriteString(ansi.ClearProgramStatusID("holon/items"))
			want.WriteString(ansi.SetProgramStatus(tea.ProgramStatus{App: "holon", ID: "holon", Message: tc.message, State: tc.state}))
			if reports != want.String() {
				t.Fatalf("OSC reports = %q, want %q", reports, want.String())
			}
		})
	}
}
