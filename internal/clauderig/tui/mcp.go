// The MCP screen lists Claude Code MCP servers across scopes and edits them.
// Following the dashboard pattern, the model only records the chosen action
// (add/remove/enable/disable); the command layer performs it after the program
// exits — running the add form or writing files outside the event loop — then
// re-opens the screen with a fresh snapshot.
package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/rigsmith/rigsmith/core/brand"
	"github.com/rigsmith/rigsmith/internal/clauderig/mcp"
	"github.com/rigsmith/rigsmith/internal/clauderig/settings"
)

var redC = lipgloss.NewStyle().Foreground(brand.Red)

// MCPAction is the intent the MCP screen records on exit. Kind "" means the user
// backed out. Name/Scope identify the target for remove/enable/disable.
type MCPAction struct {
	Kind  string // "" · "add" · "remove" · "enable" · "disable"
	Name  string
	Scope settings.Scope
}

// MCPModel is the MCP management screen.
type MCPModel struct {
	entries []mcp.Entry
	cursor  int
	hasRepo bool   // project/local scopes available (we're inside a repo)
	note    string // transient line from the last action, e.g. "removed foo"
	Action  MCPAction
}

// NewMCP builds the screen over a server snapshot. hasRepo gates the project/local
// scopes; note is an optional status line carried over from the previous action.
func NewMCP(entries []mcp.Entry, hasRepo bool, note string) MCPModel {
	return MCPModel{entries: entries, hasRepo: hasRepo, note: note}
}

func (m MCPModel) Init() tea.Cmd { return nil }

// Update drives the list: ↑/↓ (k/j) move; a adds; x removes; e/d enable/disable a
// project server; q/esc back. Enable/disable are inert on non-project rows.
func (m MCPModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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
		if m.cursor < len(m.entries)-1 {
			m.cursor++
		}
	case "a":
		m.Action = MCPAction{Kind: "add"}
		return m, tea.Quit
	case "x", "delete", "backspace":
		if e, ok := m.current(); ok {
			m.Action = MCPAction{Kind: "remove", Name: e.Name, Scope: e.Scope}
			return m, tea.Quit
		}
	case "e":
		if e, ok := m.current(); ok && e.Scope == settings.Project {
			m.Action = MCPAction{Kind: "enable", Name: e.Name, Scope: e.Scope}
			return m, tea.Quit
		}
	case "d":
		if e, ok := m.current(); ok && e.Scope == settings.Project {
			m.Action = MCPAction{Kind: "disable", Name: e.Name, Scope: e.Scope}
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m MCPModel) current() (mcp.Entry, bool) {
	if m.cursor < 0 || m.cursor >= len(m.entries) {
		return mcp.Entry{}, false
	}
	return m.entries[m.cursor], true
}

func (m MCPModel) View() string {
	// Erase on exit: the command is about to act and re-render.
	if m.Action.Kind != "" {
		return ""
	}
	var b strings.Builder
	b.WriteString(brand.ClaudeBanner("") + "\n\n")
	b.WriteString(header.Render("clauderig") + "  " + dim.Render("MCP servers") + "\n\n")
	if m.note != "" {
		b.WriteString("  " + okC.Render(m.note) + "\n\n")
	}

	if len(m.entries) == 0 {
		b.WriteString("  " + dim.Render("no MCP servers configured — press a to add one") + "\n")
	} else {
		b.WriteString("  " + dim.Render(fmt.Sprintf("%-8s %-16s %-9s %-9s %s", "SCOPE", "NAME", "TRANSPORT", "STATE", "TARGET")) + "\n")
		for i, e := range m.entries {
			cursor := "  "
			name := fmt.Sprintf("%-16s", e.Name)
			if i == m.cursor {
				cursor = cursorC.Render("▸ ")
				name = selected.Render(name)
			}
			row := fmt.Sprintf("%-8s %s %-9s %-9s %s",
				string(e.Scope), name, e.Server.Transport(),
				renderState(e.State), dim.Render(truncate(e.Server.Summary(), 40)))
			b.WriteString(cursor + row + "\n")
		}
	}

	b.WriteString("\n")
	if !m.hasRepo {
		b.WriteString("  " + dim.Render("(not in a repo — only user-scope servers shown; cd into a repo for project/local)") + "\n")
	}
	b.WriteString("\n" + dim.Render("↑/↓ move · a add · e enable · d disable · x remove · q back") + "\n")
	return b.String()
}

// renderState colors a project server's approval state; user/local show a dash.
func renderState(s mcp.State) string {
	switch s {
	case mcp.StateEnabled:
		return okC.Render("enabled")
	case mcp.StateDisabled:
		return redC.Render("disabled")
	case mcp.StatePending:
		return warnC.Render("pending")
	default:
		return dim.Render("—")
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}
