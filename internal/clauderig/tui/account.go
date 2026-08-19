// The accounts screen lists the Claude Code logins clauderig tracks and marks
// the live one. Following the dashboard/MCP pattern, the model only records the
// chosen intent (add/run/switch/remove/repair) on exit; the command layer
// performs it outside the event loop — execing claude, swapping the live
// credential, capturing the current login, recapturing from a session profile,
// or removing an account — then re-opens the screen (except `run`, which is
// terminal).
package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rigsmith/rigsmith/core/brand"
	"github.com/rigsmith/rigsmith/internal/clauderig/account"
)

// AccountAction is the intent the accounts screen records on exit. Kind "" means
// the user backed out. ID identifies the target for run/switch/remove/repair.
type AccountAction struct {
	Kind string // "" · "add" · "run" · "switch" · "remove" · "repair" · "toggle-disable" · "alias"
	ID   string
}

// AccountModel is the accounts management screen.
type AccountModel struct {
	accounts  []account.StoredStatus
	procs     []account.Instance // live Claude Code processes (block a switch)
	showProcs bool               // toggled with `p`
	cursor    int
	note      string // transient line from the last action
	Action    AccountAction
}

// NewAccount builds the screen over a snapshot of tracked accounts with their
// health (which one is live, whether each stored credential still has tokens,
// whether each session profile authenticates); procs are the live Claude Code
// processes a switch must contend with; note is carried from the prior action.
func NewAccount(accounts []account.StoredStatus, procs []account.Instance, note string) AccountModel {
	return AccountModel{accounts: accounts, procs: procs, note: note}
}

func (m AccountModel) Init() tea.Cmd { return nil }

// Update drives the list: ↑/↓ (k/j) move; enter/r runs the selected account as a
// session; s swaps the machine-wide login to it; a captures the current login;
// f recaptures a dead stored credential from its session profile (inert unless
// the row is actually repairable); d holds it out of rotation or returns it;
// n names it (sets an alias); q/esc back. All row actions are inert on an empty
// list.
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
	case "f":
		if a, ok := m.current(); ok && repairable(a) {
			m.Action = AccountAction{Kind: "repair", ID: a.ID}
			return m, tea.Quit
		}
	case "d":
		if a, ok := m.current(); ok {
			m.Action = AccountAction{Kind: "toggle-disable", ID: a.ID}
			return m, tea.Quit
		}
	case "n":
		if a, ok := m.current(); ok {
			m.Action = AccountAction{Kind: "alias", ID: a.ID}
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

func (m AccountModel) current() (account.StoredStatus, bool) {
	if m.cursor < 0 || m.cursor >= len(m.accounts) {
		return account.StoredStatus{}, false
	}
	return m.accounts[m.cursor], true
}

// repairable means `f` can help: the stored credential would be refused by a
// switch, but the account's session profile still authenticates, so its tokens
// can be recaptured.
func repairable(a account.StoredStatus) bool {
	return !a.CredentialTokens && a.Session == account.SessionOK
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

	anyRepairable, anyDeadStuck := false, false
	for i, a := range m.accounts {
		cursor := "  "
		live := "  "
		if a.Active {
			live = okC.Render("→ ")
		}
		name := accountName(a.Account)
		if i == m.cursor {
			cursor = cursorC.Render("▸ ")
			name = selected.Render(name)
		}
		sub := ""
		if a.SubscriptionType != "" {
			sub = dim.Render("  " + a.SubscriptionType)
		}
		health := ""
		if !a.CredentialTokens {
			health = errC.Render("  ✗ no tokens")
			if repairable(a) {
				anyRepairable = true
			} else {
				anyDeadStuck = true
			}
		}
		if a.Disabled {
			// Dim the whole row's meaning rather than just appending a word: a
			// disabled account is still live-capable, just not automatically
			// chosen, and the listing should make that obvious at a glance.
			health += dim.Render("  (disabled)")
		}
		switch a.Session {
		case account.SessionOK:
			health += dim.Render("  session ✓")
		case account.SessionNoTokens:
			health += warnC.Render("  session ✗")
		case account.SessionUnknown:
			health += warnC.Render("  session ?")
		}
		b.WriteString(fmt.Sprintf("%s%s%s%s%s\n", cursor, live, name, sub, health))
	}
	if anyRepairable {
		b.WriteString("\n  " + dim.Render("✗ a stored credential has no tokens — press f on it to recapture from its session") + "\n")
	}
	if anyDeadStuck {
		b.WriteString("\n  " + dim.Render("✗ no tokens and no live session — log in as that account, then press a") + "\n")
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

	keys := "↑/↓ move · enter start claude code · s switch · a add · d " + disableWord(m) + " · n alias · x remove"
	if anyRepairable {
		keys += " · f repair"
	}
	if len(m.procs) > 0 {
		keys += " · p procs"
	}
	keys += " · q back"
	b.WriteString("\n" + dim.Render(keys) + "\n")
	return b.String()
}

// disableWord names what `d` would do to the row under the cursor, so the hint
// is never the opposite of the effect.
func disableWord(m AccountModel) string {
	if a, ok := m.current(); ok && a.Disabled {
		return "enable"
	}
	return "disable"
}

func toggleWord(showing bool) string {
	if showing {
		return "hide"
	}
	return "list"
}

// accountName renders the account: its alias first when it has one — that is the
// handle the user chose and will type — then the email, which is the identity.
func accountName(a account.Account) string {
	name := a.Email
	if name == "" {
		name = a.ID
	}
	if a.Alias != "" {
		return a.Alias + " · " + name
	}
	return name
}
