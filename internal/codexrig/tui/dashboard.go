// Package tui is codexrig's interactive hub: a bare `codexrig` on a terminal
// lands here, sees where it stands, and is pointed at the one thing worth doing
// next.
//
// The model records INTENT and nothing else. Every action runs after the event
// loop has exited, in the command layer, because a sync inside Update would
// freeze the screen for as long as it took and a prompt inside it would fight
// bubbletea for the terminal.
package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/rigsmith/rigsmith/core/brand"
	"github.com/rigsmith/rigsmith/internal/codexrig/status"
)

var (
	header   = lipgloss.NewStyle().Bold(true).Underline(true)
	dim      = lipgloss.NewStyle().Foreground(brand.Muted)
	okC      = lipgloss.NewStyle().Foreground(brand.Green)
	warnC    = lipgloss.NewStyle().Foreground(brand.Yellow)
	errC     = lipgloss.NewStyle().Foreground(brand.Red)
	nextC    = lipgloss.NewStyle().Foreground(brand.Green).Bold(true)
	selected = lipgloss.NewStyle().Foreground(brand.AccentCodex).Bold(true)
	cursorC  = lipgloss.NewStyle().Foreground(brand.AccentCodex)
)

type action struct {
	key         string
	hotkey      string
	label       string
	desc        string
	recommended bool
}

// Model is the dashboard.
type Model struct {
	info   status.Info
	report status.Report
	items  []action
	cursor int

	// Chosen is the verb the user picked, or "" if they quit.
	Chosen string
}

// New builds the dashboard for a snapshot and its verdict.
func New(info status.Info, rep status.Report) Model {
	m := Model{info: info, report: rep}
	m.items = actionsFor(info)
	want := recommendedKey(info, rep)
	for i := range m.items {
		if m.items[i].key == want {
			m.items[i].recommended = true
			m.cursor = i
		}
	}
	return m
}

func actionsFor(info status.Info) []action {
	items := []action{
		{key: "init", hotkey: "i", label: "Set up", desc: "pick a private remote and install the hooks"},
	}
	if info.Remote != "" || info.HasStaging {
		items = append(items, action{key: "sync", hotkey: "s", label: "Sync", desc: "capture this machine's setup and push it"})
	}
	if info.LastSync != "" {
		items = append(items, action{key: "restore", hotkey: "r", label: "Restore", desc: "write the synced setup onto this machine"})
	}
	items = append(items,
		action{key: "status", hotkey: "t", label: "Status", desc: "the full picture"},
		action{key: "account", hotkey: "a", label: "Accounts", desc: "run several Codex logins side by side"},
		action{key: "doctor", hotkey: "d", label: "Doctor", desc: "check that the backup is actually working"},
	)
	return items
}

// recommendedKey picks the one thing worth doing next. It follows the same
// priority the status verdict uses, so the screen and the summary line can never
// suggest different things.
func recommendedKey(info status.Info, rep status.Report) string {
	switch {
	case !info.HasStaging && info.Remote == "":
		return "init"
	case rep.Level == status.Red:
		return "doctor"
	case rep.Action == "codexrig pull":
		return "sync" // pull has no tile; sync reconciles on the way through
	case rep.Action != "":
		return strings.TrimPrefix(rep.Action, "codexrig ")
	default:
		return "status"
	}
}

func (m Model) Init() tea.Cmd { return nil }

// Update records what was chosen. Deliberately no work here — see the package
// comment.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "q", "esc", "ctrl+c":
		return m, tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.items)-1 {
			m.cursor++
		}
	case "enter":
		m.Chosen = m.items[m.cursor].key
		return m, tea.Quit
	default:
		for _, it := range m.items {
			if it.hotkey == key.String() {
				m.Chosen = it.key
				return m, tea.Quit
			}
		}
	}
	return m, nil
}

// View renders the screen, and renders NOTHING once something is chosen — so
// the command that runs next starts on a clean terminal rather than under a
// stale dashboard.
func (m Model) View() string {
	if m.Chosen != "" {
		return ""
	}
	var b strings.Builder
	b.WriteString(brand.CodexBanner(""))
	b.WriteString("\n\n")
	b.WriteString(m.panel())
	b.WriteString("\n")

	style := okC
	switch m.report.Level {
	case status.Amber:
		style = warnC
	case status.Red:
		style = errC
	}
	fmt.Fprintf(&b, "  %s %s\n\n", style.Render("●"), m.report.Summary)

	for i, it := range m.items {
		cursor := "  "
		label := it.label
		if i == m.cursor {
			cursor = cursorC.Render("› ")
			label = selected.Render(it.label)
		}
		tag := ""
		if it.recommended {
			tag = " " + nextC.Render("next")
		}
		fmt.Fprintf(&b, "%s%-10s %s%s\n", cursor, label, dim.Render(it.desc), tag)
	}
	b.WriteString("\n")
	b.WriteString(dim.Render("  ↑/↓ move · enter select · " + strings.Join(hotkeys(m.items), "/") + " shortcut · q quit"))
	b.WriteString("\n")
	return b.String()
}

// hotkeys builds the legend FROM the items, so a new action cannot be added
// without its key appearing — the sibling tool's legend lost one that way and
// nothing noticed.
func hotkeys(items []action) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.hotkey)
	}
	return out
}

func (m Model) panel() string {
	var b strings.Builder
	fmt.Fprintf(&b, "  %s  %s (%s)\n", header.Render("codexrig"), m.info.Machine.Name, m.info.Machine.OS)

	remote := dim.Render("none — set up first")
	if m.info.Remote != "" {
		remote = m.info.Remote
	}
	fmt.Fprintf(&b, "  %-10s %s\n", "remote", remote)

	last := dim.Render("never")
	if m.info.LastSync != "" {
		last = m.info.LastSync
	}
	fmt.Fprintf(&b, "  %-10s %s\n", "last sync", last)

	if m.info.Account.LoggedOut {
		fmt.Fprintf(&b, "  %-10s %s\n", "login", dim.Render("not logged in"))
	} else if m.info.Account.Email != "" {
		fmt.Fprintf(&b, "  %-10s %s\n", "login", m.info.Account.Email)
	}

	carrying := dim.Render("config only")
	if m.info.Sessions {
		carrying = "config and sessions"
	}
	fmt.Fprintf(&b, "  %-10s %s\n", "carrying", carrying)

	for _, r := range m.info.Roots {
		if !r.Present {
			fmt.Fprintf(&b, "  %-10s %s\n", r.ID, dim.Render("absent here"))
			continue
		}
		fmt.Fprintf(&b, "  %-10s %d files\n", r.ID, r.Files)
	}
	if n := len(m.info.Devices); n > 1 {
		fmt.Fprintf(&b, "  %-10s %d\n", "devices", n)
	}
	return b.String()
}
