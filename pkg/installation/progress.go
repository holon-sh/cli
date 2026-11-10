package installation

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/opencontainers/go-digest"
	"golang.org/x/sync/errgroup"
	"golang.org/x/term"
	"k8s.io/utils/clock"
)

type progressState = tea.ProgramState

const (
	progressIdle    = tea.ProgramStateIdle
	progressWorking = tea.ProgramStateWorking
	progressDone    = tea.ProgramStateDone
	progressBlocked = tea.ProgramStateBlocked
	progressError   = tea.ProgramStateError
)

const progressItemsID = "holon/items"

type progressOutput struct {
	mu       sync.Mutex
	program  *tea.Program
	writer   io.Writer
	verbose  bool
	clock    clock.PassiveClock
	renderer errgroup.Group
	statuses map[string]tea.ProgramStatus
	preview  string
	printed  string
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
	output.program.Send(tea.RawMsg{Msg: ansi.ClearProgramStatusID(progressStatusID(""))})
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
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	status := tea.ProgramStatus{App: "holon", ID: progressStatusID(name), Title: name, Message: phase, State: state}
	if o.statuses[name] == status {
		return
	}
	o.statuses[name] = status
	o.publish(ansi.SetProgramStatus(status), strings.TrimSpace(name+" "+phase))
}

// problem shows err on the named row, or removes the row when err is nil. A task that never failed stays silent;
// plain output, which cannot remove a line it already printed, reports the recovery instead.
func (o *progressOutput) problem(name string, err error) {
	if err != nil {
		o.status(name, err.Error(), progressError)
		return
	}
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if _, shown := o.statuses[name]; !shown {
		return
	}
	delete(o.statuses, name)
	o.publish(ansi.ClearProgramStatusID(progressStatusID(name)), name+" ok")
}

// publish renders a status change: the rows and an OSC report on a terminal, or one line of plain output.
func (o *progressOutput) publish(report, line string) {
	if o.program == nil {
		_, _ = fmt.Fprintln(o.writer, line)
		return
	}
	o.program.Send(slices.SortedFunc(maps.Values(o.statuses), func(a, b tea.ProgramStatus) int {
		return strings.Compare(a.Title, b.Title)
	}))
	o.program.Send(tea.RawMsg{Msg: report})
}

func progressStatusID(title string) string {
	if title == "" {
		return "holon"
	}
	return progressItemsID + "/" + digest.FromBytes([]byte(title)).Encoded()[:16]
}

type progressPreview string

func (o *progressOutput) endpoints(
	stdout io.Writer, heading string, manager *Client, name string, apps []manifestApp, provisional bool,
) error {
	block, err := manager.summary(name, apps)
	if err != nil || block == "" {
		return err
	}
	text := "\n" + heading + "\n" + block
	if o == nil {
		_, err = io.WriteString(stdout, text)
		return err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.program != nil {
		if !provisional {
			_, _ = o.Write([]byte(text))
			text = ""
		}
		o.preview = text
		o.program.Send(progressPreview(text))
		return nil
	}
	if block == o.printed {
		text = heading + "\n"
	}
	o.printed = block
	_, err = io.WriteString(stdout, text)
	return err
}

func (o *progressOutput) finish(err error, message string) error {
	if o.program != nil {
		o.mu.Lock()
		if o.preview != "" {
			_, _ = o.Write([]byte(o.preview))
			o.program.Send(progressPreview(""))
		}
		o.mu.Unlock()
		state := progressDone
		if err != nil {
			state, message = progressError, err.Error()
			if errors.Is(err, context.Canceled) {
				state, message = progressIdle, "Canceled"
			}
		}
		delete(o.statuses, "")
		for name, item := range o.statuses {
			if item.State == progressWorking {
				item.State, item.Message = state, message
				o.statuses[name] = item
			}
		}
		o.program.Send(tea.RawMsg{Msg: ansi.ClearProgramStatusID(progressItemsID)})
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
	preview string
	items   []tea.ProgramStatus
	status  tea.ProgramStatus
	spinner spinner.Model
}

func newProgressModel() progressModel {
	return progressModel{spinner: spinner.New(spinner.WithSpinner(spinner.MiniDot))}
}

func (m progressModel) Init() tea.Cmd { return nil }

func (m progressModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	if preview, ok := message.(progressPreview); ok {
		m.preview = string(preview)
		return m, nil
	}
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
	content.WriteString(m.preview)
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

var stepTime = regexp.MustCompile(`(?m)^\d+\.\d+ ?`)

// diagnosis is what a failed build printed about its failure: the output that buildkit repeats for the step that failed,
// without its timestamps, which holds the failing tool's own error, or else the build's last line.
func (l *buildLog) diagnosis() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	output := strings.TrimSpace(string(l.tail))
	if _, step, ok := strings.Cut(output, "\n------\n > "); ok {
		_, step, _ = strings.Cut(step, ":\n")
		step, _, _ = strings.Cut(step, "\n------")
		return strings.TrimSpace(stepTime.ReplaceAllString(step, ""))
	}
	return output[strings.LastIndexByte(output, '\n')+1:]
}

// explain adds the diagnosis of what failed to err.
func (l *buildLog) explain(err error) error {
	if diagnosis := l.diagnosis(); diagnosis != "" {
		return fmt.Errorf("%w:\n%s", err, diagnosis)
	}
	return err
}

// finished reports app's build as phase, or as failed with err and the build's diagnosis.
func (l *buildLog) finished(output *progressOutput, app, phase string, err error) error {
	state := progressWorking
	if errors.Is(err, context.Canceled) {
		phase, state = "stopped", progressIdle
	} else if err != nil {
		err, phase, state = l.explain(err), "failed", progressError
	}
	output.status(app, phase, state)
	return err
}

type progressFunc func([]byte) (int, error)

func (f progressFunc) Write(data []byte) (int, error) { return f(data) }
