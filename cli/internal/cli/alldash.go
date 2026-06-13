package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"
)

// The live `--all` dashboard. The dev verb runs across every workspace package
// in topo order; instead of streaming each package's output straight to the
// terminal, this renders a single bubbletea program with per-package status and
// the running package's streamed output. The packages run sequentially in a
// goroutine (dependency order preserved) and feed the program; ctrl+c cancels
// the remaining packages and kills the running command. Used only on an
// interactive TTY (not --quiet/--dry-run); otherwise the plain sequential path
// runs.

const maxAllOutput = 8

// allTask is one package's command to run.
type allTask struct {
	name string
	eco  string
	dir  string // absolute
	rel  string // dir relative to the repo root, for display
	argv []string
}

type allState int

const (
	allPending allState = iota
	allRunning
	allOK
	allFailed
	allSkipped
)

type allRow struct {
	task  allTask
	state allState
}

// ---- messages (from the runner goroutine) ------------------------------

type (
	allStarted  struct{ idx int }
	allOutput   struct{ line string }
	allFinished struct {
		idx int
		ok  bool
	}
	allSkippedMsg struct{ idx int }
	allDoneMsg    struct{}
)

type allModel struct {
	verb    string
	rows    []allRow
	spin    spinner.Model
	cancel  context.CancelFunc
	running int
	output  []string

	done      bool
	cancelled bool
	okCount   int
	failCount int
}

func newAllModel(verb string, tasks []allTask, cancel context.CancelFunc) allModel {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("14"))
	rows := make([]allRow, len(tasks))
	for i, t := range tasks {
		rows[i] = allRow{task: t}
	}
	return allModel{verb: verb, rows: rows, spin: sp, cancel: cancel, running: -1}
}

func (m allModel) Init() tea.Cmd { return m.spin.Tick }

func (m allModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	case allStarted:
		m.rows[msg.idx].state = allRunning
		m.running, m.output = msg.idx, nil
		return m, nil
	case allOutput:
		m.output = append(m.output, msg.line)
		if len(m.output) > maxAllOutput {
			m.output = m.output[len(m.output)-maxAllOutput:]
		}
		return m, nil
	case allFinished:
		if msg.ok {
			m.rows[msg.idx].state = allOK
			m.okCount++
		} else {
			m.rows[msg.idx].state = allFailed
			m.failCount++
		}
		m.running, m.output = -1, nil
		return m, nil
	case allSkippedMsg:
		m.rows[msg.idx].state = allSkipped
		return m, nil
	case allDoneMsg:
		m.done = true
		return m, tea.Quit
	case tea.KeyMsg:
		if !m.done && (msg.String() == "ctrl+c" || msg.String() == "q") {
			m.cancelled = true
			if m.cancel != nil {
				m.cancel()
			}
		}
		return m, nil
	}
	return m, nil
}

var (
	allDoneStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true)
	allFailStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true)
	allDimStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
)

func (m allModel) View() string {
	var b strings.Builder
	title := m.verb + " --all"
	if m.done {
		fmt.Fprintf(&b, "%s\n", allDimStyle.Render("── "+title+" ─────────────────────────"))
	} else {
		fmt.Fprintf(&b, "%s %s\n", m.spin.View(), allDimStyle.Render(title+" — "+m.verb+"ing "+pluralN(len(m.rows), "package")))
	}

	for i, r := range m.rows {
		b.WriteString(m.rowLine(i, r))
		b.WriteByte('\n')
		if i == m.running {
			for _, line := range m.output {
				fmt.Fprintf(&b, "%s\n", allDimStyle.Render("    "+line))
			}
		}
	}

	b.WriteByte('\n')
	if m.done {
		summary := allDoneStyle.Render(fmt.Sprintf("✓ %d ok", m.okCount))
		if m.failCount > 0 {
			summary += "   " + allFailStyle.Render(fmt.Sprintf("✗ %d failed", m.failCount))
		}
		if m.cancelled {
			summary += "   " + allDimStyle.Render("(cancelled)")
		}
		b.WriteString(summary)
	} else {
		b.WriteString(allDimStyle.Render("ctrl+c cancel"))
	}
	b.WriteByte('\n')
	return b.String()
}

func (m allModel) rowLine(i int, r allRow) string {
	var glyph, name string
	switch r.state {
	case allRunning:
		glyph, name = m.spin.View(), r.task.name
	case allOK:
		glyph, name = allDoneStyle.Render("✓"), r.task.name
	case allFailed:
		glyph, name = allFailStyle.Render("✗"), allFailStyle.Render(r.task.name)
	case allSkipped:
		glyph, name = allDimStyle.Render("–"), allDimStyle.Render(r.task.name)
	default:
		glyph, name = allDimStyle.Render("○"), r.task.name
	}
	meta := r.task.eco
	if r.task.rel != "" && r.task.rel != "." {
		meta = r.task.rel + " · " + r.task.eco
	}
	return "  " + glyph + " " + name + "  " + allDimStyle.Render(meta)
}

func pluralN(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// ---- line-buffered output sink -----------------------------------------

// lineSink splits writes into lines and calls emit per complete line. It is
// safe for the two goroutines os/exec uses for stdout and stderr.
type lineSink struct {
	mu   sync.Mutex
	buf  []byte
	emit func(string)
}

func (s *lineSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.buf = append(s.buf, p...)
	for {
		i := bytes.IndexByte(s.buf, '\n')
		if i < 0 {
			break
		}
		s.emit(strings.TrimRight(string(s.buf[:i]), "\r"))
		s.buf = s.buf[i+1:]
	}
	return len(p), nil
}

func (s *lineSink) flush() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.buf) > 0 {
		s.emit(strings.TrimRight(string(s.buf), "\r"))
		s.buf = nil
	}
}

// allDashboardEligible reports whether the live `--all` dashboard should render:
// a real run on an interactive terminal.
func allDashboardEligible() bool {
	return !dryRun && !quiet && term.IsTerminal(os.Stdout.Fd()) && term.IsTerminal(os.Stdin.Fd())
}

// runAcrossDashboard runs the tasks under the live dashboard and returns an
// error when any package failed (or the run was cancelled).
func runAcrossDashboard(cmd *cobra.Command, tasks []allTask, verb string) error {
	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()

	prog := tea.NewProgram(newAllModel(verb, tasks, cancel),
		tea.WithInput(cmd.InOrStdin()), tea.WithOutput(cmd.OutOrStdout()))

	go func() {
		for i, t := range tasks {
			if ctx.Err() != nil {
				prog.Send(allSkippedMsg{i})
				continue
			}
			prog.Send(allStarted{i})
			ok := runAllTask(ctx, t, func(line string) { prog.Send(allOutput{line}) })
			prog.Send(allFinished{idx: i, ok: ok})
		}
		prog.Send(allDoneMsg{})
	}()

	final, err := prog.Run()
	if err != nil {
		return err
	}
	fm := final.(allModel)
	if fm.failCount > 0 {
		return fmt.Errorf("%s failed in %s", verb, pluralN(fm.failCount, "package"))
	}
	// A user-initiated cancel (no failures) exits cleanly — the dashboard already
	// rendered "(cancelled)".
	return nil
}

// runAllTask runs one package's command, streaming combined output line-by-line
// through emit, and returns whether it succeeded. The command inherits the rig
// env layering (presets, .env, config) like runCommand.
func runAllTask(ctx context.Context, t allTask, emit func(string)) bool {
	c := exec.CommandContext(ctx, t.argv[0], t.argv[1:]...)
	c.Dir = t.dir
	c.Env = commandEnv(t.dir)
	sink := &lineSink{emit: emit}
	c.Stdout, c.Stderr = sink, sink
	err := c.Run()
	sink.flush()
	return err == nil
}
