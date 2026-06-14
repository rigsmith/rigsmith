package cli

import (
	"io"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/rigsmith/core/brand"
	"github.com/rigsmith/shiprig/internal/pipeline"
)

// The live release dashboard. The headless pipeline runs in a goroutine and
// drives this single bubbletea program through a Reporter→tea.Msg bridge
// (dashReporter); confirm gates round-trip through dashPrompter so one program
// owns the terminal the whole run. Used only for an interactive, rich TTY run;
// every other mode uses the sequential plain/rich reporters.

const maxDashOutput = 6 // streamed output lines kept under the running step

type stepStatus int

const (
	statusPending stepStatus = iota
	statusRunning
	statusOK
	statusSkipped
	statusFailed
	statusCancelled
)

type dashStep struct {
	name   string
	status stepStatus
	reason string
}

// ---- messages (sent from the pipeline goroutine) -----------------------

type (
	dashStepStarted   struct{ name string }
	dashStepSkipped   struct{ name, reason string }
	dashStepCompleted struct{ name string }
	dashStepCancelled struct{ name string }
	dashCmdStarted    struct{ line string }
	dashCmdOutput     struct{ lines []string }
	dashStepFailed    struct{ name string }
	dashRunCompleted  struct {
		success bool
		message string
	}
	dashConfirm struct {
		message string
		resp    chan bool
	}
)

type dashboardModel struct {
	tool   string
	steps  []dashStep
	byName map[string]int
	spin   spinner.Model

	running int // index of the running step, -1 when none
	cmdLine string
	output  []string

	confirming  bool
	confirmMsg  string
	confirmResp chan bool

	done    bool
	success bool
	message string
}

func newDashboardModel(steps []pipeline.ResolvedStep, tool string) dashboardModel {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(brand.Cyan)

	ds := make([]dashStep, len(steps))
	byName := make(map[string]int, len(steps))
	for i, s := range steps {
		st := statusPending
		reason := ""
		if !s.Enabled() {
			st, reason = statusSkipped, s.SkipReason
		}
		ds[i] = dashStep{name: s.Name, status: st, reason: reason}
		byName[s.Name] = i
	}
	return dashboardModel{tool: tool, steps: ds, byName: byName, spin: sp, running: -1}
}

func (m dashboardModel) Init() tea.Cmd { return m.spin.Tick }

func (m *dashboardModel) set(name string, status stepStatus, reason string) {
	if i, ok := m.byName[name]; ok {
		m.steps[i].status = status
		m.steps[i].reason = reason
		if status == statusRunning {
			m.running = i
		}
	}
}

func (m dashboardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case dashStepStarted:
		m.set(msg.name, statusRunning, "")
		m.cmdLine, m.output = "", nil
		return m, nil
	case dashStepSkipped:
		m.set(msg.name, statusSkipped, msg.reason)
		return m, nil
	case dashStepCompleted:
		m.set(msg.name, statusOK, "")
		m.running, m.cmdLine, m.output = -1, "", nil
		return m, nil
	case dashStepCancelled:
		m.set(msg.name, statusCancelled, "")
		return m, nil
	case dashStepFailed:
		m.set(msg.name, statusFailed, "")
		return m, nil
	case dashCmdStarted:
		m.cmdLine, m.output = msg.line, nil
		return m, nil
	case dashCmdOutput:
		m.output = append(m.output, msg.lines...)
		if len(m.output) > maxDashOutput {
			m.output = m.output[len(m.output)-maxDashOutput:]
		}
		return m, nil
	case dashConfirm:
		m.confirming = true
		m.confirmMsg = msg.message
		m.confirmResp = msg.resp
		return m, nil
	case dashRunCompleted:
		m.done = true
		m.success = msg.success
		m.message = msg.message
		m.running = -1
		return m, tea.Quit

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m dashboardModel) handleKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.confirming {
		switch key.String() {
		case "y", "Y", "enter":
			m.answer(true)
		case "n", "N", "esc", "ctrl+c":
			m.answer(false)
		}
		return m, nil
	}
	if m.done {
		return m, tea.Quit
	}
	// During active execution, ctrl+c is intentionally ignored: a release can't
	// be safely torn down mid-command (a half-published package). Decline at the
	// next confirm gate to stop cleanly.
	return m, nil
}

func (m *dashboardModel) answer(v bool) {
	if m.confirmResp != nil {
		m.confirmResp <- v
		m.confirmResp = nil
	}
	m.confirming = false
}

func (m dashboardModel) View() string {
	if m.done {
		return m.finalView()
	}

	var b []byte
	b = append(b, ruleStyle.Render("── Release ──────────────────────────────")...)
	b = append(b, '\n')

	for i, s := range m.steps {
		b = append(b, m.stepLine(i, s)...)
		b = append(b, '\n')
		// Under the running step, show the current command + streamed output.
		if i == m.running {
			if m.cmdLine != "" {
				b = append(b, skipStyle.Render("    $ "+m.cmdLine)...)
				b = append(b, '\n')
			}
			for _, line := range m.output {
				b = append(b, skipStyle.Render("      "+line)...)
				b = append(b, '\n')
			}
		}
	}

	b = append(b, '\n')
	if m.confirming {
		b = append(b, cancelStyle.Render("⏸ "+m.confirmMsg+"  ")...)
		b = append(b, editorDim.Render("[y/N]")...)
	} else {
		b = append(b, editorDim.Render("release running… (ctrl+c to abort only at a confirm gate)")...)
	}
	b = append(b, '\n')
	return string(b)
}

func (m dashboardModel) stepLine(i int, s dashStep) string {
	var glyph, name string
	switch s.status {
	case statusRunning:
		glyph, name = m.spin.View(), s.name
	case statusOK:
		glyph, name = stepOkStyle.Render("✓"), s.name
	case statusFailed:
		glyph, name = failStyle.Render("✗"), failStyle.Render(s.name)
	case statusCancelled:
		glyph, name = cancelStyle.Render("⊘"), s.name
	case statusSkipped:
		glyph, name = skipStyle.Render("–"), skipStyle.Render(s.name)
	default: // pending
		glyph, name = skipStyle.Render("○"), s.name
	}
	line := "  " + glyph + " " + name
	if s.status == statusSkipped && s.reason != "" {
		line += "  " + skipStyle.Render("("+s.reason+")")
	}
	return line
}

func (m dashboardModel) finalView() string {
	switch {
	case m.success:
		body := "Release complete."
		if m.message != "" {
			body = m.message
		}
		return successPanel.Render(stepOkStyle.Render(body)) + "\n"
	default:
		body := "Release failed."
		if m.message != "" {
			body = m.message
		}
		if name := m.lastTouched(); name != "" {
			body += "\nResume with: " + m.tool + " release --from " + name
		}
		// A clean cancel (a declined gate) reads better in the cancel palette.
		if m.cancelled() {
			return cancelPanel.Render(cancelStyle.Render(body)) + "\n"
		}
		return failPanel.Render(failStyle.Render(body)) + "\n"
	}
}

// lastTouched returns the last step that started (failed/cancelled/running) for
// the resume hint.
func (m dashboardModel) lastTouched() string {
	for i := len(m.steps) - 1; i >= 0; i-- {
		switch m.steps[i].status {
		case statusFailed, statusCancelled, statusRunning:
			return m.steps[i].name
		}
	}
	return ""
}

func (m dashboardModel) cancelled() bool {
	for _, s := range m.steps {
		if s.status == statusCancelled {
			return true
		}
	}
	return false
}

// ---- Reporter / Prompter bridges ---------------------------------------

// dashReporter implements pipeline.Reporter by forwarding events to the
// dashboard program. Output is masked before it leaves for the UI. It tracks
// the current step so a command failure (reported by label, not step) can be
// attributed to the right row.
type dashReporter struct {
	prog    *tea.Program
	masker  *pipeline.SecretMasker
	current string
}

func (r *dashReporter) Plan(steps []pipeline.ResolvedStep, dryRun bool) {} // model is pre-seeded
func (r *dashReporter) StepStarted(name string) {
	r.current = name
	r.prog.Send(dashStepStarted{name})
}
func (r *dashReporter) StepSkipped(name, reason string) {
	r.prog.Send(dashStepSkipped{name, reason})
}
func (r *dashReporter) CommandStarted(label string, command pipeline.CommandSpec) {
	r.prog.Send(dashCmdStarted{r.masker.Mask(pipeline.DescribeCommand(command))})
}
func (r *dashReporter) CommandOutput(lines []string) {
	masked := make([]string, len(lines))
	for i, l := range lines {
		masked[i] = r.masker.Mask(l)
	}
	r.prog.Send(dashCmdOutput{masked})
}
func (r *dashReporter) CommandFailed(label string, exitCode int) {
	r.prog.Send(dashStepFailed{r.current})
}
func (r *dashReporter) StepCompleted(name string) { r.prog.Send(dashStepCompleted{name}) }
func (r *dashReporter) StepCancelled(name string) { r.prog.Send(dashStepCancelled{name}) }
func (r *dashReporter) RunCompleted(success bool, message string) {
	r.prog.Send(dashRunCompleted{success, r.masker.Mask(message)})
}

// dashPrompter implements pipeline.Prompter by round-tripping a confirm request
// through the dashboard (which renders the prompt inline and replies).
type dashPrompter struct{ prog *tea.Program }

func (p dashPrompter) Confirm(message string) bool {
	resp := make(chan bool, 1)
	p.prog.Send(dashConfirm{message: message, resp: resp})
	return <-resp
}

// runDashboard runs the pipeline under the live dashboard, returning the run's
// success. newPipeline builds the pipeline wired to the given reporter/prompter
// (the caller captures the runner, masker, work dir, and native handlers). The
// pipeline executes in a goroutine and feeds the program; the program owns the
// terminal until the run completes.
func runDashboard(
	steps []pipeline.ResolvedStep,
	cfg *pipeline.Config,
	tool string,
	in io.Reader,
	out io.Writer,
	masker *pipeline.SecretMasker,
	newPipeline func(pipeline.Reporter, pipeline.Prompter) *pipeline.Pipeline,
) (bool, error) {
	prog := tea.NewProgram(newDashboardModel(steps, tool), tea.WithInput(in), tea.WithOutput(out))
	p := newPipeline(&dashReporter{prog: prog, masker: masker}, dashPrompter{prog: prog})

	resultCh := make(chan bool, 1)
	go func() { resultCh <- p.Run(steps, cfg, false) }()

	if _, err := prog.Run(); err != nil {
		return false, err
	}
	return <-resultCh, nil
}
