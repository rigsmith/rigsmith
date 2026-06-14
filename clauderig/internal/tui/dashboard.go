// Package tui holds clauderig's bubbletea interfaces. The dashboard is the hub:
// it shows the gathered sync status above a navigable action menu (the same
// cursor/enter list the other rig tools use) and dispatches to an action
// (init/sync/restore/status) on selection. Following the changerig pattern, the
// model only records the chosen action; the command runs it after the program
// exits, so heavy work never runs inside the event loop.
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
	header   = lipgloss.NewStyle().Bold(true).Underline(true)
	dim      = lipgloss.NewStyle().Foreground(brand.Muted)
	okC      = lipgloss.NewStyle().Foreground(brand.Green)
	warnC    = lipgloss.NewStyle().Foreground(brand.Yellow)
	nextC    = lipgloss.NewStyle().Bold(true).Foreground(brand.Green)
	selected = lipgloss.NewStyle().Bold(true).Foreground(brand.Cyan)
	cursorC  = lipgloss.NewStyle().Foreground(brand.Cyan)
)

// action is one selectable dashboard verb. key is the value recorded in Chosen
// (and dispatched by the ui command); hotkey is the accelerator letter.
type action struct {
	key         string
	hotkey      string
	label       string
	desc        string
	recommended bool // the suggested next step for the current state
}

// Model is the dashboard. Chosen is the action selected on exit ("" = none).
type Model struct {
	info   status.Info
	items  []action
	cursor int
	hint   string
	Chosen string
}

// New builds a dashboard over a gathered status snapshot, assembling the action
// list for the current state and placing the cursor on the recommended step.
func New(info status.Info) Model {
	items := actionsFor(info)
	cursor := 0
	for i, a := range items {
		if a.recommended {
			cursor = i
			break
		}
	}
	return Model{info: info, items: items, cursor: cursor, hint: nextStepFor(info)}
}

// canSync reports whether a sync is possible — it needs a remote to push to.
func canSync(info status.Info) bool { return info.Remote != "" }

// canRestore reports whether there's a snapshot to restore from (a synced commit
// in the staging repo). Without one, restore has nothing to write.
func canRestore(info status.Info) bool { return info.LastSync != "" }

// actionsFor builds the state-driven action list: init and status always apply;
// sync needs a remote; restore needs a snapshot. The suggested next step is
// flagged recommended.
func actionsFor(info status.Info) []action {
	rec := recommendedKey(info)
	var items []action
	add := func(key, hotkey, label, desc string) {
		items = append(items, action{key: key, hotkey: hotkey, label: label, desc: desc, recommended: key == rec})
	}
	add("init", "i", "Init", "configure remote, machine identity, roots, and hooks")
	if canSync(info) {
		add("sync", "s", "Sync", "snapshot, redact, rewrite, and push your setup")
	}
	if canRestore(info) {
		add("restore", "r", "Restore", "restore your setup here, path-corrected for this OS")
	}
	add("status", "t", "Status", "show sync state: remote, last sync, roots, hooks")
	return items
}

// recommendedKey picks the suggested next action from the state: set up a remote
// first, then a first sync, then keep it current — or, once synced and clean,
// restore your setup onto another machine.
func recommendedKey(info status.Info) string {
	switch {
	case info.Remote == "":
		return "init"
	case info.LastSync == "":
		return "sync"
	case info.Dirty:
		return "sync"
	default:
		return "restore"
	}
}

// nextStepFor is the context-aware suggestion shown above the action list.
func nextStepFor(info status.Info) string {
	switch {
	case info.Remote == "":
		return "No remote yet — Init sets one up."
	case info.LastSync == "":
		return "Never synced — Sync snapshots and pushes your setup."
	case info.Dirty:
		return "Local changes not pushed — Sync to update."
	default:
		return "Up to date — Restore your setup on another machine."
	}
}

// nextStep is retained for callers/tests that want the hint for a model's state.
func (m Model) nextStep() string { return nextStepFor(m.info) }

func (m Model) Init() tea.Cmd { return nil }

// Update drives the menu: ↑/↓ (k/j) move the cursor, enter selects; the action
// hotkeys (i/s/r/t) are accelerators that select directly — and because only
// available actions are in the list, an unavailable hotkey is inert. q/esc quit.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch k.String() {
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
		for _, a := range m.items {
			if k.String() == a.hotkey {
				m.Chosen = a.key
				return m, tea.Quit
			}
		}
	}
	return m, nil
}

func (m Model) View() string {
	// Once an action is picked the program is quitting and about to run that
	// command — erase the dashboard so the command's output starts on a clean
	// screen instead of below a stale menu. (Plain quit keeps the dashboard.)
	if m.Chosen != "" {
		return ""
	}
	var b strings.Builder
	b.WriteString(m.statusPanel())
	b.WriteString("\n")
	b.WriteString(nextC.Render("  → ") + dim.Render(m.hint) + "\n\n")

	for i, a := range m.items {
		cursor := "  "
		label := a.label
		if i == m.cursor {
			cursor = cursorC.Render("▸ ")
			label = selected.Render(label)
		}
		row := label + " " + dim.Render("("+a.hotkey+")")
		if a.recommended {
			row += "  " + nextC.Render("next")
		}
		b.WriteString(fmt.Sprintf("%s%s  %s\n", cursor, row, dim.Render(a.desc)))
	}

	b.WriteString("\n" + dim.Render("↑/↓ move · enter select · i/s/r/t shortcut · q quit") + "\n")
	return b.String()
}

// statusPanel renders the read-only sync snapshot shown above the action list.
func (m Model) statusPanel() string {
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
	return b.String()
}
