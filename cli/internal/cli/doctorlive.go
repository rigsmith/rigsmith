package cli

import (
	"os"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"
)

// The live `rig doctor` checklist. Each check runs concurrently as a bubbletea
// Cmd (so probes that shell out don't block the UI); a row spins until its probe
// resolves, then shows ✓/!/✗ + detail. Used on an interactive TTY; CI / piped /
// --quiet runs print the static checklist instead.

// doctorLiveEligible reports whether the live checklist should render.
func doctorLiveEligible() bool {
	return !quiet && term.IsTerminal(os.Stdout.Fd())
}

type docRow struct {
	label  string
	done   bool
	result check
}

// checkDoneMsg carries a finished check's result back to the model.
type checkDoneMsg struct {
	idx    int
	result check
}

type doctorModel struct {
	pending  []pendingCheck
	rows     []docRow
	spin     spinner.Model
	doneN    int
	severity docLevel
}

func newDoctorModel(checks []pendingCheck) doctorModel {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("14"))
	rows := make([]docRow, len(checks))
	for i, c := range checks {
		rows[i] = docRow{label: c.label}
	}
	return doctorModel{pending: checks, rows: rows, spin: sp}
}

func (m doctorModel) Init() tea.Cmd {
	cmds := make([]tea.Cmd, 0, len(m.pending)+1)
	cmds = append(cmds, m.spin.Tick)
	for i := range m.pending {
		cmds = append(cmds, m.checkCmd(i))
	}
	return tea.Batch(cmds...)
}

// checkCmd runs one check off the UI goroutine and reports its result.
func (m doctorModel) checkCmd(i int) tea.Cmd {
	run := m.pending[i].run
	return func() tea.Msg { return checkDoneMsg{idx: i, result: run()} }
}

func (m doctorModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	case checkDoneMsg:
		m.rows[msg.idx].done = true
		m.rows[msg.idx].result = msg.result
		if msg.result.level > m.severity {
			m.severity = msg.result.level
		}
		m.doneN++
		if m.doneN == len(m.rows) {
			return m, tea.Quit
		}
		return m, nil
	case tea.KeyMsg:
		if s := msg.String(); s == "ctrl+c" || s == "q" {
			return m, tea.Quit
		}
		return m, nil
	}
	return m, nil
}

func (m doctorModel) View() string {
	var b []byte
	for _, r := range m.rows {
		var glyph, detail string
		if r.done {
			glyph = renderMark(r.result.level)
			detail = dimStyle.Render(r.result.detail)
		} else {
			glyph = m.spin.View()
			detail = dimStyle.Render("checking…")
		}
		b = append(b, ("  " + glyph + " " + pad(r.label, 10) + " " + detail + "\n")...)
	}
	if m.doneN == len(m.rows) {
		b = append(b, '\n')
		b = append(b, doctorSummary(m.severity)...)
		b = append(b, '\n')
	}
	return string(b)
}

// runDoctorLive runs the checklist program and returns the worst severity seen.
// On a program error it falls back to running the checks synchronously.
func runDoctorLive(cmd *cobra.Command, checks []pendingCheck) docLevel {
	final, err := tea.NewProgram(newDoctorModel(checks),
		tea.WithInput(cmd.InOrStdin()), tea.WithOutput(cmd.OutOrStdout())).Run()
	if err != nil {
		severity := docOK
		for _, pc := range checks {
			if c := pc.run(); c.level > severity {
				severity = c.level
			}
		}
		return severity
	}
	return final.(doctorModel).severity
}
