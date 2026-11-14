package installation

import (
	"bytes"
	"context"
	"errors"
	"io"
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"k8s.io/utils/clock"
)

func TestProgressTransitions(t *testing.T) {
	for _, tc := range []struct {
		name, message string
		state         tea.ProgramState
		err           error
	}{
		{name: "terminal canceled", err: context.Canceled, state: "idle", message: "Canceled"},
		{name: "terminal succeeded", state: "done", message: "Finished"},
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
				output.working("alpha")
				output.problem("editor imports", errors.New("write failed"))
				output.headline("Watching for changes", progressIdle)
			}
			output.problem("editor imports", nil)
			output.problem("never reported", nil)
			output.problem("remote deployment", errors.New("update failed"))
			if err := output.finish(tc.err, "Finished"); err != nil {
				t.Fatal(err)
			}
			visible := strings.Join(strings.Fields(ansi.Strip(stream.String())), " ")
			for _, want := range []string{"alpha not updated", "remote deployment update failed", tc.message} {
				if !strings.Contains(visible, want) {
					t.Fatalf("final terminal status = %q, missing %q", visible, want)
				}
			}

			reports := strings.Join(regexp.MustCompile("\x1b]7501;[^\x07]*\x07").FindAllString(stream.String(), -1), "")
			var want strings.Builder
			for _, report := range []string{
				ansi.SetProgramStatus(tea.ProgramStatus{App: "holon", ID: progressStatusID("alpha"), Title: "alpha", State: tea.ProgramStateWorking}),
				ansi.SetProgramStatus(tea.ProgramStatus{App: "holon", ID: progressStatusID("editor imports"), Title: "editor imports", Message: "write failed", State: tea.ProgramStateError}),
				ansi.SetProgramStatus(tea.ProgramStatus{App: "holon", ID: "holon", Message: "Watching for changes", State: tea.ProgramStateIdle}),
				ansi.ClearProgramStatusID(progressStatusID("editor imports")),
				ansi.SetProgramStatus(tea.ProgramStatus{App: "holon", ID: progressStatusID("remote deployment"), Title: "remote deployment", Message: "update failed", State: tea.ProgramStateError}),
			} {
				want.WriteString(report)
			}
			want.WriteString(ansi.ClearProgramStatusID("holon/items"))
			want.WriteString(ansi.SetProgramStatus(tea.ProgramStatus{App: "holon", ID: "holon", Message: tc.message, State: tc.state}))
			if reports != want.String() {
				t.Fatalf("OSC reports = %q, want %q", reports, want.String())
			}
		})
	}
}
