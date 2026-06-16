// The accounts screen lists the Claude Code logins clauderig tracks and marks
// the live one. Following the dashboard/MCP pattern, the model only records the
// chosen intent (add/run/switch) on exit; the command layer performs it outside
// the event loop — execing claude, swapping the live credential, or capturing
// the current login — then re-opens the screen (except `run`, which is terminal).
package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rigsmith/rigsmith/core/brand"
	"github.com/rigsmith/rigsmith/internal/clauderig/account"
)

// AccountAction is the intent the accounts screen records on exit. Kind "" means
// the user backed out. ID identifies the target for run/switch.
type AccountAction struct {
	Kind string // "" · "add" · "run" · "switch"
	ID   string
}

// AccountModel is the accounts management screen.
type AccountModel struct {
	accounts  []account.Account
	activeID  string
	procs     []account.Instance // live Claude Code processes (block a switch)
	showProcs bool               // toggled with `p`
	cursor    int
	note      string // transient line from the last action
	Action    AccountAction
}

// NewAccount builds the screen over a snapshot of tracked accounts. activeID is
// the account clauderig tracks as the live login (""=none); procs are the live
// Claude Code processes a switch must contend with; note is carried from the
// prior action.
func NewAccount(accounts []account.Account, activeID string, procs []account.Instance, note string) AccountModel {
	return AccountModel{accounts: accounts, activeID: activeID, procs: procs, note: note}
}

func (m AccountModel) Init() tea.Cmd { return nil }

// Update drives the list: ↑/↓ (k/j) move; enter/r runs the selected account as a
// session; s swaps the machine-wide login to it; a captures the current login;
// q/esc back. run/switch are inert on an empty list.
func (m AccountModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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
		if m.cursor < len(m.accounts)-1 {
			m.cursor++
		}
	case "a":
		m.Action = AccountAction{Kind: "add"}
		return m, tea.Quit
	case "enter", "r":
		if a, ok := m.current(); ok {
			m.Action = AccountAction{Kind: "run", ID: a.ID}
			return m, tea.Quit
		}
	case "s":
		if a, ok := m.current(); ok {
			m.Action = AccountAction{Kind: "switch", ID: a.ID}
			return m, tea.Quit
		}
	case "x", "delete", "backspace":
		if a, ok := m.current(); ok {
			m.Action = AccountAction{Kind: "remove", ID: a.ID}
			return m, tea.Quit
		}
	case "p":
		if len(m.procs) > 0 {
			m.showProcs = !m.showProcs // toggle the live-process list inline
		}
	}
	return m, nil
}

func (m AccountModel) current() (account.Account, bool) {
	if m.cursor < 0 || m.cursor >= len(m.accounts) {
		return account.Account{}, false
	}
	return m.accounts[m.cursor], true
}

func (m AccountModel) View() string {
	// Erase on exit: the command is about to act (and re-render or exec claude).
	if m.Action.Kind != "" {
		return ""
	}
	var b strings.Builder
	b.WriteString(brand.ClaudeBanner("") + "\n\n")
	b.WriteString(header.Render("clauderig") + "  " + dim.Render("accounts") + "\n\n")
	if m.note != "" {
		b.WriteString("  " + m.note + "\n\n")
	}

	if len(m.accounts) == 0 {
		b.WriteString("  " + dim.Render("no accounts yet — press a to capture the current login") + "\n")
		b.WriteString("\n" + dim.Render("a add · q back") + "\n")
		return b.String()
	}

	for i, a := range m.accounts {
		cursor := "  "
		live := "  "
		if a.ID == m.activeID {
			live = okC.Render("→ ")
		}
		name := accountName(a)
		if i == m.cursor {
			cursor = cursorC.Render("▸ ")
			name = selected.Render(name)
		}
		sub := ""
		if a.SubscriptionType != "" {
			sub = dim.Render("  " + a.SubscriptionType)
		}
		b.WriteString(fmt.Sprintf("%s%s%s%s\n", cursor, live, name, sub))
	}

	if n := len(m.procs); n > 0 {
		b.WriteString("\n  " + warnC.Render(fmt.Sprintf("⚠ %d Claude Code process(es) live", n)) +
			dim.Render(" — switch will offer force/kill (p to "+toggleWord(m.showProcs)+")") + "\n")
		if m.showProcs {
			for _, p := range m.procs {
				b.WriteString("    " + dim.Render(fmt.Sprintf("pid %d  %s", p.PID, p.Kind)) + "\n")
			}
		}
	}

	keys := "↑/↓ move · enter run · s switch · a add · x remove · q back"
	if len(m.procs) > 0 {
		keys = "↑/↓ move · enter run · s switch · a add · x remove · p procs · q back"
	}
	b.WriteString("\n" + dim.Render(keys) + "\n")
	return b.String()
}

func toggleWord(showing bool) string {
	if showing {
		return "hide"
	}
	return "list"
}

// accountName renders "label (id)" or just the id when unlabeled.
func accountName(a account.Account) string {
	if a.Label == "" {
		return a.ID
	}
	return fmt.Sprintf("%-14s %s", a.Label, dim.Render(a.ID))
}
