package cli

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// confirmPrune shows the prune plan and returns the scope to act on plus whether
// to proceed. With toggles enabled (no --worktrees/--branches flag) the w/b/a keys
// retarget the plan in place — preview the worktree-only or branch-only sweep, or
// return to all — and the chosen scope is what runs. Cancel (esc/q/ctrl+c) or any
// model error returns proceed=false.
func confirmPrune(initial pruneScope, toggles bool, preview func(pruneScope) (string, pruneCounts)) (scope pruneScope, proceed bool) {
	text, counts := preview(initial)
	res, err := tea.NewProgram(pruneConfirmModel{
		scope:   initial,
		toggles: toggles,
		preview: preview,
		text:    text,
		counts:  counts,
	}).Run()
	if err != nil {
		return initial, false
	}
	m, ok := res.(pruneConfirmModel)
	if !ok {
		return initial, false
	}
	return m.scope, m.proceed
}

// pruneConfirmModel is the bubbletea confirm screen: the plan for the current
// scope, a summary, and key hints. w/b/a retarget (when toggles is set), enter
// confirms the shown scope, esc cancels.
type pruneConfirmModel struct {
	scope    pruneScope
	toggles  bool
	preview  func(pruneScope) (string, pruneCounts)
	text     string
	counts   pruneCounts
	proceed  bool
	quitting bool
}

func (m pruneConfirmModel) Init() tea.Cmd { return nil }

func (m pruneConfirmModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "enter", "y":
		m.proceed, m.quitting = true, true
		return m, tea.Quit
	case "esc", "q", "ctrl+c":
		m.proceed, m.quitting = false, true
		return m, tea.Quit
	case "w":
		return m.retarget(scopeWorktrees), nil
	case "b":
		return m.retarget(scopeBranches), nil
	case "a":
		return m.retarget(scopeBoth), nil
	}
	return m, nil
}

// retarget re-previews for scope s, when toggling is allowed and the scope
// actually changed.
func (m pruneConfirmModel) retarget(s pruneScope) pruneConfirmModel {
	if !m.toggles || s == m.scope {
		return m
	}
	m.scope = s
	m.text, m.counts = m.preview(s)
	return m
}

func (m pruneConfirmModel) View() string {
	if m.quitting {
		return "" // clear our frame; the command prints the result next
	}
	title := "Prune plan"
	switch m.scope {
	case scopeWorktrees:
		title += " — worktrees only"
	case scopeBranches:
		title += " — branches only"
	}
	var b strings.Builder
	b.WriteString(HeaderStyle.Render(title) + "\n\n")
	b.WriteString(m.text + "\n\n")
	b.WriteString(DimStyle.Render(m.counts.summary(m.scope, true)) + "\n\n")
	b.WriteString(m.footer())
	return b.String()
}

var pruneKeyStyle = lipgloss.NewStyle().Foreground(brandCyan).Bold(true)

func (m pruneConfirmModel) footer() string {
	act := "prune"
	switch m.scope {
	case scopeWorktrees:
		act = "prune worktrees"
	case scopeBranches:
		act = "prune branches"
	default:
		if m.toggles {
			act = "prune all"
		}
	}
	hints := []string{pruneKeyHint("enter", act)}
	if m.toggles {
		switch m.scope {
		case scopeBoth:
			hints = append(hints, pruneKeyHint("w", "worktrees only"), pruneKeyHint("b", "branches only"))
		case scopeWorktrees:
			hints = append(hints, pruneKeyHint("b", "branches only"), pruneKeyHint("a", "all"))
		case scopeBranches:
			hints = append(hints, pruneKeyHint("w", "worktrees only"), pruneKeyHint("a", "all"))
		}
	}
	hints = append(hints, pruneKeyHint("esc", "cancel"))
	return strings.Join(hints, DimStyle.Render("   ·   "))
}

func pruneKeyHint(key, label string) string {
	return pruneKeyStyle.Render("["+key+"]") + " " + DimStyle.Render(label)
}
