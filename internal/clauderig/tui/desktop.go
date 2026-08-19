// The Desktop screen lists the Claude Desktop profiles clauderig manages and
// marks the ones whose windows are open. It follows the accounts-screen pattern:
// the model only records the chosen intent on exit, and the command layer
// performs it outside the event loop — launching the app, quitting an instance,
// creating a profile, or deleting one — then re-opens the screen.
//
// Unlike the accounts screen there is no "live" account here, because there
// isn't one: every profile is a separate login and any number can be signed in
// at once. `●` therefore means "window open", not "this is the current one" —
// the whole point of the per-profile model.
package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rigsmith/rigsmith/core/brand"
)

// DesktopRow is one profile as the screen needs it: identity plus whether its
// window is currently open. The command layer resolves this, so the TUI never
// shells out to look for processes while the event loop is running.
type DesktopRow struct {
	Name      string
	Email     string
	AccountID string // linked `clauderig account` id, if any (a label)
	Open      bool
	// OpenUnknown means the process scan failed, so whether this profile's
	// window is open could not be established. Rendered distinctly from
	// "closed": the difference decides whether deleting it is safe.
	OpenUnknown bool
	// Shared reports that this profile's session history is linked to the
	// shared tree, and so is covered by `clauderig sync`.
	Shared bool
}

// Label renders a row's identity.
func (r DesktopRow) Label() string {
	if r.Email != "" {
		return r.Name + " · " + r.Email
	}
	return r.Name
}

// DesktopAction is the intent the screen records on exit. Kind "" means the user
// backed out.
type DesktopAction struct {
	Kind string // "" · "add" · "open" · "quit" · "remove" · "toggle-share"
	Name string
}

// DesktopModel is the Desktop profiles screen.
type DesktopModel struct {
	rows      []DesktopRow
	installed bool
	supported bool
	cursor    int
	note      string
	Action    DesktopAction
}

// NewDesktop builds the screen over a snapshot of profiles. installed/supported
// describe the platform, so the screen can explain an empty list that is empty
// for a reason rather than inviting an action that cannot work.
func NewDesktop(rows []DesktopRow, installed, supported bool, note string) DesktopModel {
	return DesktopModel{rows: rows, installed: installed, supported: supported, note: note}
}

func (m DesktopModel) Init() tea.Cmd { return nil }

// Update drives the list: ↑/↓ (k/j) move; enter/o opens or focuses the selected
// profile; c closes it; a creates a profile to log into; s shares or unshares its
// session history; x deletes one; q/esc back. Every action is inert when Claude
// Desktop isn't available.
func (m DesktopModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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
		if m.cursor < len(m.rows)-1 {
			m.cursor++
		}
	case "a":
		if m.installed {
			m.Action = DesktopAction{Kind: "add"}
			return m, tea.Quit
		}
	case "enter", "o":
		if r, ok := m.current(); ok && m.installed {
			m.Action = DesktopAction{Kind: "open", Name: r.Name}
			return m, tea.Quit
		}
	case "c":
		// Closing is only meaningful for a window that is open; offering it on a
		// closed profile would report "not open" as though something happened.
		if r, ok := m.current(); ok && r.Open && !r.OpenUnknown {
			m.Action = DesktopAction{Kind: "quit", Name: r.Name}
			return m, tea.Quit
		}
	case "s":
		// Sharing repoints a directory Electron holds open, so it is offered
		// only for a profile known to be closed.
		if r, ok := m.current(); ok && !r.Open && !r.OpenUnknown {
			m.Action = DesktopAction{Kind: "toggle-share", Name: r.Name}
			return m, tea.Quit
		}
	case "x", "delete", "backspace":
		if r, ok := m.current(); ok {
			m.Action = DesktopAction{Kind: "remove", Name: r.Name}
			return m, tea.Quit
		}
	}
	return m, nil
}

// shareWord names what `s` would do to the row under the cursor, so the hint is
// never the opposite of the effect. A profile that is open cannot be relinked at
// all, and the hint says so rather than offering an inert key.
func shareWord(m DesktopModel) string {
	r, ok := m.current()
	switch {
	case !ok:
		return "s share"
	case r.Open || r.OpenUnknown:
		return dim.Render("s share (close it first)")
	case r.Shared:
		return "s unshare"
	default:
		return "s share"
	}
}

func (m DesktopModel) current() (DesktopRow, bool) {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return DesktopRow{}, false
	}
	return m.rows[m.cursor], true
}

func (m DesktopModel) View() string {
	if m.Action.Kind != "" {
		return "" // erase on exit: the command is about to act and re-render
	}
	var b strings.Builder
	b.WriteString(brand.ClaudeBanner("") + "\n\n")
	b.WriteString(header.Render("clauderig") + "  " + dim.Render("desktop profiles") + "\n\n")
	if m.note != "" {
		b.WriteString("  " + m.note + "\n\n")
	}

	// An unavailable platform is a dead end, not an empty list — say which.
	if !m.supported {
		b.WriteString("  " + dim.Render("Claude Desktop is not available on this platform — `clauderig account` works everywhere") + "\n")
		b.WriteString("\n" + dim.Render("q back") + "\n")
		return b.String()
	}
	if !m.installed {
		b.WriteString("  " + warnC.Render("Claude Desktop is not installed") +
			dim.Render(" — install it from https://claude.ai/download") + "\n")
		b.WriteString("\n" + dim.Render("q back") + "\n")
		return b.String()
	}
	if len(m.rows) == 0 {
		b.WriteString("  " + dim.Render("no profiles yet — press a to create one and log into it") + "\n")
		b.WriteString("\n" + dim.Render("a add · q back") + "\n")
		return b.String()
	}

	anyOpen := false
	for i, r := range m.rows {
		cursor := "  "
		state := dim.Render("  closed")
		switch {
		case r.OpenUnknown:
			state = warnC.Render("  unknown (process scan failed)")
		case r.Open:
			state = okC.Render("  open")
			anyOpen = true
		}
		name := r.Label()
		if i == m.cursor {
			cursor = cursorC.Render("▸ ")
			name = selected.Render(name)
		}
		marker := "  "
		switch {
		case r.OpenUnknown:
			marker = warnC.Render("? ")
		case r.Open:
			marker = okC.Render("● ")
		}
		link := ""
		if r.Shared {
			link += dim.Render("  shared history")
		}
		if r.AccountID != "" {
			link += dim.Render("  ↔ " + r.AccountID)
		}
		b.WriteString(fmt.Sprintf("%s%s%s%s%s\n", cursor, marker, name, state, link))
	}

	b.WriteString("\n  " + dim.Render("each profile is its own login — opening one never signs another out") + "\n")

	keys := "↑/↓ move · enter open · a add · " + shareWord(m) + " · x remove"
	if anyOpen {
		keys = "↑/↓ move · enter open · c close · a add · " + shareWord(m) + " · x remove"
	}
	keys += " · q back"
	b.WriteString("\n" + dim.Render(keys) + "\n")
	return b.String()
}
