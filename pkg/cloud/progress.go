package cloud

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"golang.org/x/sync/errgroup"
	"golang.org/x/term"
	"k8s.io/utils/clock"

	"github.com/holon-sh/cli/internal/clientutil"
)

type progressState = tea.ProgramState

const (
	progressIdle    = tea.ProgramStateIdle
	progressWorking = tea.ProgramStateWorking
	progressDone    = tea.ProgramStateDone
	progressBlocked = tea.ProgramStateBlocked
	progressError   = tea.ProgramStateError
)

type progressOutput struct {
	mu       sync.Mutex
	program  *tea.Program
	writer   io.Writer
	verbose  bool
	clock    clock.PassiveClock
	renderer errgroup.Group
	statuses map[string]tea.ProgramStatus
}

func newProgressOutput(stdout, stderr io.Writer, verbose bool, clock clock.PassiveClock) (*progressOutput, io.Writer, io.Writer) {
	output := &progressOutput{
		writer: &buildLog{writer: stderr}, verbose: verbose, clock: clock,
		statuses: map[string]tea.ProgramStatus{"": {}},
	}
	_, outTerminal := terminalFile(stdout)
	_, errTerminal := terminalFile(stderr)
	if !outTerminal || !errTerminal {
		return output, stdout, output.writer
	}
	output.program = tea.NewProgram(newProgressModel(), tea.WithOutput(stderr), tea.WithInput(nil), tea.WithoutSignalHandler())
	output.writer = output
	output.renderer.Go(func() error {
		_, err := output.program.Run()
		return err
	})
	output.program.Send(tea.RawMsg{Msg: ansi.ClearProgramStatusID("holon")})
	return output, output, output
}

func terminalFile(stream any) (*os.File, bool) {
	file, ok := stream.(*os.File)
	return file, ok && term.IsTerminal(int(file.Fd()))
}

func (o *progressOutput) Write(data []byte) (int, error) {
	o.program.Send(tea.Printf("%s", strings.TrimSuffix(string(data), "\n"))())
	return len(data), nil
}

func (o *progressOutput) status(name, phase string, state tea.ProgramState) {
	o.report(tea.ProgramStatus{Title: name, Message: phase, State: state})
}

func (o *progressOutput) report(status tea.ProgramStatus) {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	status.App, status.ID = "holon", "holon"
	if status.Title != "" {
		status.ID += "/items/" + clientutil.HashBytes([]byte(status.Title))[:16]
	}
	if o.statuses[status.Title] == status {
		return
	}
	o.statuses[status.Title] = status
	if o.program != nil {
		o.program.Send(slices.SortedFunc(maps.Values(o.statuses), func(a, b tea.ProgramStatus) int {
			return strings.Compare(a.Title, b.Title)
		}))
		o.program.Send(tea.RawMsg{Msg: ansi.SetProgramStatus(status)})
	} else {
		_, _ = fmt.Fprintln(o.writer, strings.TrimSpace(status.Title+" "+status.Message))
	}
}

func (o *progressOutput) finish(err error, message string) error {
	if o.program != nil {
		state := progressDone
		if err != nil {
			state, message = progressError, err.Error()
			if errors.Is(err, context.Canceled) {
				state, message = progressIdle, "Canceled"
			}
		}
		delete(o.statuses, "")
		for name, item := range o.statuses {
			if slices.Contains([]progressState{progressWorking, progressBlocked}, item.State) {
				item.State, item.Message = state, message
				o.statuses[name] = item
			}
		}
		o.program.Send(tea.RawMsg{Msg: ansi.ClearProgramStatusID("holon/items")})
		o.status("", message, state)
		o.program.Send(tea.Quit())
	}
	return o.renderer.Wait()
}

func (o *progressOutput) logs(writer io.Writer) *buildLog {
	return &buildLog{writer: writer, quiet: o != nil && !o.verbose}
}

func (o *progressOutput) measure(name, phase string) func() {
	if o == nil || !o.verbose {
		return func() {}
	}
	started := o.clock.Now()
	return func() {
		_, _ = fmt.Fprintf(o.writer, "Timing %s %s: %s\n", name, phase, o.clock.Since(started).Round(time.Millisecond))
	}
}

type progressModel struct {
	items   []tea.ProgramStatus
	status  tea.ProgramStatus
	spinner spinner.Model
}

func newProgressModel() progressModel {
	return progressModel{spinner: spinner.New(spinner.WithSpinner(spinner.MiniDot))}
}

func (m progressModel) Init() tea.Cmd { return nil }

func (m progressModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	if statuses, ok := message.([]tea.ProgramStatus); ok {
		m.status, m.items = statuses[0], statuses[1:]
		return m, m.spinner.Tick
	}
	var command tea.Cmd
	m.spinner, command = m.spinner.Update(message)
	if !m.working() {
		command = nil
	}
	return m, command
}

func (m progressModel) working() bool {
	if m.status.State == progressBlocked {
		return false
	}
	return m.status.State == progressWorking || slices.ContainsFunc(m.items, func(s tea.ProgramStatus) bool { return s.State == progressWorking })
}

func (m progressModel) indicator(state progressState) string {
	switch state {
	case progressWorking:
		if m.status.State == progressBlocked {
			return "·"
		}
		return m.spinner.View()
	case progressDone:
		return "✓"
	case progressError:
		return "✗"
	case progressBlocked:
		return "!"
	default:
		return " "
	}
}

func (m progressModel) View() tea.View {
	var content strings.Builder
	for _, item := range m.items {
		_, _ = fmt.Fprintf(&content, "  %s %s  %s\n", m.indicator(item.State), item.Title, item.Message)
	}
	if m.status.Message != "" {
		_, _ = fmt.Fprintf(&content, "%s %s", m.indicator(m.status.State), m.status.Message)
	}
	return tea.NewView(content.String())
}

type buildLog struct {
	mu     sync.Mutex
	writer io.Writer
	quiet  bool
	tail   []byte
}

func (l *buildLog) Write(data []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.quiet {
		return l.writer.Write(data)
	}
	tail := data[max(0, len(data)-maxCommandOutput):]
	l.tail = append(slices.Delete(l.tail, 0, max(0, len(l.tail)+len(tail)-maxCommandOutput)), tail...)
	return len(data), nil
}

func (l *buildLog) failed(app string) {
	if len(l.tail) > 0 {
		_, _ = fmt.Fprintf(l.writer, "\nBuild diagnostics (%s):\n%s\n", app, strings.TrimSpace(string(l.tail)))
	}
}

func (l *buildLog) finished(output *progressOutput, app, phase string, err error) {
	state := progressWorking
	if errors.Is(err, context.Canceled) {
		phase, state = "stopped", progressIdle
	} else if err != nil {
		l.failed(app)
		phase, state = "failed", progressError
	}
	output.status(app, phase, state)
}
