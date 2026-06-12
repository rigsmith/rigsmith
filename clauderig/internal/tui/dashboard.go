// Package tui holds clauderig's bubbletea interfaces. The dashboard is the hub:
// it shows the gathered status and dispatches to an action (sync/restore/status)
// on a hotkey. Following the changerig pattern, the model only records the chosen
// action; the command runs it after the program exits, so heavy work never runs
// inside the event loop.
package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/rigsmith/clauderig/internal/status"
)

var (
	header = lipgloss.NewStyle().Bold(true).Underline(true)
	dim    = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	okC    = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	warnC  = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	keyC   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
)

// Model is the dashboard. Chosen is the action selected on exit ("" = none).
type Model struct {
	info   status.Info
	Chosen string
}

// New builds a dashboard over a gathered status snapshot.
func New(info status.Info) Model { return Model{info: info} }

func (m Model) Init() tea.Cmd { return nil }

// Update handles hotkeys: q/esc quit; s/r/t pick an action and quit.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		case "s":
			m.Chosen = "sync"
			return m, tea.Quit
		case "r":
			m.Chosen = "restore"
			return m, tea.Quit
		case "t":
			m.Chosen = "status"
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m Model) View() string {
	var b strings.Builder
	b.WriteString(header.Render("clauderig") + "  " +
		dim.Render(m.info.Machine.Name+" ("+m.info.Machine.OS+")") + "\n\n")

	remote := m.info.Remote
	if remote == "" {
		remote = dim.Render("none — run clauderig init")
	}
	b.WriteString("  remote    " + remote + "\n")

	last := m.info.LastSync
	if last == "" {
		last = dim.Render("never")
	}
	b.WriteString("  last sync " + last + "\n")
	if m.info.Dirty {
		b.WriteString("            " + warnC.Render("uncommitted changes") + "\n")
	}

	b.WriteString(dim.Render("  roots:") + "\n")
	for _, r := range m.info.Roots {
		if !r.Present {
			b.WriteString(fmt.Sprintf("  %-8s %s\n", r.ID, dim.Render("absent here")))
			continue
		}
		b.WriteString(fmt.Sprintf("  %-8s %d files\n", r.ID, r.Files))
	}

	hk := dim.Render("not installed")
	if len(m.info.Hooks) > 0 {
		hk = okC.Render(strings.Join(m.info.Hooks, ", "))
	}
	b.WriteString("  hooks     " + hk + "\n\n")

	b.WriteString("  " + keyC.Render("s") + " sync   " + keyC.Render("r") + " restore   " +
		keyC.Render("t") + " status   " + keyC.Render("q") + " quit\n")
	return b.String()
}
