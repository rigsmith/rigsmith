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
	"github.com/rigsmith/core/brand"
)

var (
	header = lipgloss.NewStyle().Bold(true).Underline(true)
	dim    = lipgloss.NewStyle().Foreground(brand.Muted)
	okC    = lipgloss.NewStyle().Foreground(brand.Green)
	warnC  = lipgloss.NewStyle().Foreground(brand.Yellow)
	keyC   = lipgloss.NewStyle().Bold(true).Foreground(brand.AccentClaude)
	nextC  = lipgloss.NewStyle().Bold(true).Foreground(brand.Green)
)

// Model is the dashboard. Chosen is the action selected on exit ("" = none).
type Model struct {
	info   status.Info
	Chosen string
}

// New builds a dashboard over a gathered status snapshot.
func New(info status.Info) Model { return Model{info: info} }

func (m Model) Init() tea.Cmd { return nil }

// canSync reports whether a sync is possible — it needs a remote to push to.
func (m Model) canSync() bool { return m.info.Remote != "" }

// canRestore reports whether there's a snapshot to restore from (a synced
// commit in the staging repo). Without one, restore has nothing to write.
func (m Model) canRestore() bool { return m.info.LastSync != "" }

// Update handles hotkeys: q/esc quit; i (init) and t (status) always act; s
// (sync) and r (restore) only when the state makes them possible, so the
// dashboard never offers a verb that would just error.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		case "i":
			m.Chosen = "init"
			return m, tea.Quit
		case "s":
			if m.canSync() {
				m.Chosen = "sync"
				return m, tea.Quit
			}
		case "r":
			if m.canRestore() {
				m.Chosen = "restore"
				return m, tea.Quit
			}
		case "t":
			m.Chosen = "status"
			return m, tea.Quit
		}
	}
	return m, nil
}

// nextStep is the context-aware suggestion shown above the hotkey legend: set up
// a remote first, then a first sync, then keep it current — or, once synced,
// pull your setup onto another machine.
func (m Model) nextStep() string {
	switch {
	case m.info.Remote == "":
		return "No remote yet — press i to set one up (init)."
	case m.info.LastSync == "":
		return "Never synced — press s to snapshot and push your setup."
	case m.info.Dirty:
		return "Local changes not pushed — press s to sync."
	default:
		return "Up to date — press r to restore your setup on another machine."
	}
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
	b.WriteString("  hooks     " + hk + "\n")

	if len(m.info.Devices) > 0 {
		b.WriteString(dim.Render("  devices:") + "\n")
		for _, d := range m.info.Devices {
			self := ""
			if d.Name == m.info.Machine.Name {
				self = dim.Render(" (this)")
			}
			b.WriteString(fmt.Sprintf("  %-12s %s%s\n", d.Name, dim.Render(d.OS), self))
		}
	}
	b.WriteString("\n")

	b.WriteString(nextC.Render("  → ") + dim.Render(m.nextStep()) + "\n\n")

	// Legend lists only the actions that currently apply: init/status always, but
	// sync only with a remote and restore only with a snapshot to pull.
	keys := []string{keyC.Render("i") + " init"}
	if m.canSync() {
		keys = append(keys, keyC.Render("s")+" sync")
	}
	if m.canRestore() {
		keys = append(keys, keyC.Render("r")+" restore")
	}
	keys = append(keys, keyC.Render("t")+" status", keyC.Render("q")+" quit")
	b.WriteString("  " + strings.Join(keys, "   ") + "\n")
	return b.String()
}
