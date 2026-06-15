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
	accounts []account.Account
	liveID   string
	cursor   int
	note     string // transient line from the last action
	Action   AccountAction
}

// NewAccount builds the screen over a snapshot of tracked accounts. liveID marks
// the currently-live login (""=none/untracked); note is carried from the prior action.
func NewAccount(accounts []account.Account, liveID, note string) AccountModel {
	return AccountModel{accounts: accounts, liveID: liveID, note: note}
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
		if a.ID == m.liveID {
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

	b.WriteString("\n" + dim.Render("↑/↓ move · enter run (this terminal) · s switch (machine-wide) · a add · q back") + "\n")
	return b.String()
}

// accountName renders "label (id)" or just the id when unlabeled.
func accountName(a account.Account) string {
	if a.Label == "" {
		return a.ID
	}
	return fmt.Sprintf("%-14s %s", a.Label, dim.Render(a.ID))
}
