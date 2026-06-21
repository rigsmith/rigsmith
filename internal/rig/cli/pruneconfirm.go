package cli

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// confirmPrune shows the prune plan and returns the scope to act on, the set of
// kept items the user chose to force-remove, and whether to proceed. With toggles
// enabled (no --worktrees/--branches flag) the w/b/a keys retarget the plan in
// place — preview the worktree-only or branch-only sweep, or return to all — and
// the chosen scope is what runs. f opens a picker over the kept-but-forceable
// items; whatever is selected there is folded into the plan and returned as force.
// Cancel (esc/q/ctrl+c) or any model error returns proceed=false.
func confirmPrune(initial pruneScope, toggles bool, preview func(pruneScope, map[string]bool) (string, pruneCounts, []pruneRow)) (scope pruneScope, force map[string]bool, proceed bool) {
	text, counts, rows := preview(initial, nil)
	res, err := tea.NewProgram(pruneConfirmModel{
		scope:     initial,
		toggles:   toggles,
		preview:   preview,
		force:     map[string]bool{},
		text:      text,
		counts:    counts,
		forceable: forceableSkips(rows),
	}).Run()
	if err != nil {
		return initial, nil, false
	}
	m, ok := res.(pruneConfirmModel)
	if !ok {
		return initial, nil, false
	}
	return m.scope, m.force, m.proceed
}

// pruneConfirmModel is the bubbletea confirm screen: the plan for the current
// scope, a summary, and key hints. w/b/a retarget (when toggles is set), f opens
// the force-select sub-mode over the kept-but-forceable items, enter confirms the
// shown plan, esc cancels.
type pruneConfirmModel struct {
	scope   pruneScope
	toggles bool
	preview func(pruneScope, map[string]bool) (string, pruneCounts, []pruneRow)
	force   map[string]bool // names the user has chosen to force-remove
	text    string
	counts  pruneCounts

	// forceable lists the kept items the current plan could force-remove; it
	// drives the [f] sub-mode and the footer hint.
	forceable []string

	// Force-select sub-mode state.
	selecting bool
	cursor    int
	sel       map[string]bool // pending toggles within the sub-mode

	proceed  bool
	quitting bool
}

func (m pruneConfirmModel) Init() tea.Cmd { return nil }

func (m pruneConfirmModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	if m.selecting {
		return m.updateSelecting(key)
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
	case "f":
		if len(m.forceable) > 0 {
			m.selecting = true
			m.cursor = 0
			m.sel = map[string]bool{}
		}
		return m, nil
	}
	return m, nil
}

// updateSelecting drives the force-select sub-mode: move the cursor, space
// toggles an item, enter folds the selection into the plan and re-previews, esc
// backs out without changing anything.
func (m pruneConfirmModel) updateSelecting(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.forceable)-1 {
			m.cursor++
		}
	case " ", "x":
		name := m.forceable[m.cursor]
		if m.sel[name] {
			delete(m.sel, name)
		} else {
			m.sel[name] = true
		}
	case "enter":
		for name := range m.sel {
			m.force[name] = true
		}
		m.selecting = false
		return m.reload(), nil
	case "esc", "q":
		m.selecting = false
	case "ctrl+c":
		m.proceed, m.quitting = false, true
		return m, tea.Quit
	}
	return m, nil
}

// retarget re-previews for scope s, when toggling is allowed and the scope
// actually changed. The accumulated force set carries across.
func (m pruneConfirmModel) retarget(s pruneScope) pruneConfirmModel {
	if !m.toggles || s == m.scope {
		return m
	}
	m.scope = s
	return m.reload()
}

// reload re-previews the current scope with the accumulated force set, refreshing
// the plan text, counts, and the remaining forceable items.
func (m pruneConfirmModel) reload() pruneConfirmModel {
	var rows []pruneRow
	m.text, m.counts, rows = m.preview(m.scope, m.force)
	m.forceable = forceableSkips(rows)
	return m
}

func (m pruneConfirmModel) View() string {
	if m.quitting {
		return "" // clear our frame; the command prints the result next
	}
	if m.selecting {
		return m.selectView()
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

// selectView renders the force-select sub-mode: a checklist of the kept items the
// plan could force-remove.
func (m pruneConfirmModel) selectView() string {
	var b strings.Builder
	b.WriteString(HeaderStyle.Render("Force-include kept items") + "\n\n")
	b.WriteString(DimStyle.Render("Select any to remove anyway — for a worktree this also discards uncommitted changes.") + "\n\n")
	for i, name := range m.forceable {
		cursor := "  "
		if i == m.cursor {
			cursor = pruneKeyStyle.Render("▸ ")
		}
		box := DimStyle.Render("[ ]")
		if m.sel[name] {
			box = OkStyle.Render("[x]")
		}
		b.WriteString(fmt.Sprintf("%s%s %s\n", cursor, box, HeaderStyle.Render(name)))
	}
	b.WriteString("\n" + strings.Join([]string{
		pruneKeyHint("space", "toggle"),
		pruneKeyHint("enter", "force selected"),
		pruneKeyHint("esc", "back"),
	}, DimStyle.Render("   ·   ")))
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
	if len(m.forceable) > 0 {
		hints = append(hints, pruneKeyHint("f", "force a kept item"))
	}
	hints = append(hints, pruneKeyHint("esc", "cancel"))
	return strings.Join(hints, DimStyle.Render("   ·   "))
}

func pruneKeyHint(key, label string) string {
	return pruneKeyStyle.Render("["+key+"]") + " " + DimStyle.Render(label)
}
