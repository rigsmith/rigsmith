package cli

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func sendPrune(m pruneConfirmModel, msg tea.Msg) (pruneConfirmModel, tea.Cmd) {
	nm, cmd := m.Update(msg)
	return nm.(pruneConfirmModel), cmd
}

func newConfirmModel(toggles bool) (pruneConfirmModel, *[]pruneScope) {
	previewed := &[]pruneScope{}
	preview := func(s pruneScope, _ map[string]bool) (string, pruneCounts, []pruneRow) {
		*previewed = append(*previewed, s)
		return "plan", pruneCounts{worktrees: 1, branches: 2}, nil
	}
	return pruneConfirmModel{
		scope:   scopeBoth,
		toggles: toggles,
		preview: preview,
		force:   map[string]bool{},
		text:    "plan",
		counts:  pruneCounts{worktrees: 1, branches: 2},
	}, previewed
}

// With toggles on, w/b/a retarget the scope and re-preview that scope.
func TestPruneConfirm_TogglesRetargetAndPreview(t *testing.T) {
	m, previewed := newConfirmModel(true)

	m, _ = sendPrune(m, runes("w"))
	if m.scope != scopeWorktrees {
		t.Errorf("after w, scope = %v, want worktrees", m.scope)
	}
	m, _ = sendPrune(m, runes("b"))
	if m.scope != scopeBranches {
		t.Errorf("after b, scope = %v, want branches", m.scope)
	}
	m, _ = sendPrune(m, runes("a"))
	if m.scope != scopeBoth {
		t.Errorf("after a, scope = %v, want both", m.scope)
	}
	want := []pruneScope{scopeWorktrees, scopeBranches, scopeBoth}
	if len(*previewed) != len(want) {
		t.Fatalf("previews = %v, want one per toggle %v", *previewed, want)
	}
}

// Enter confirms the currently shown scope; esc cancels — both quit.
func TestPruneConfirm_EnterConfirmsCurrentScope(t *testing.T) {
	m, _ := newConfirmModel(true)
	m, _ = sendPrune(m, runes("w")) // narrow to worktrees first

	m, cmd := sendPrune(m, key(tea.KeyEnter))
	if !isQuit(cmd) {
		t.Fatal("enter must quit")
	}
	if !m.proceed || m.scope != scopeWorktrees {
		t.Errorf("enter → proceed=%v scope=%v, want true/worktrees", m.proceed, m.scope)
	}
}

func TestPruneConfirm_EscCancels(t *testing.T) {
	m, _ := newConfirmModel(true)
	m, cmd := sendPrune(m, key(tea.KeyEsc))
	if !isQuit(cmd) {
		t.Fatal("esc must quit")
	}
	if m.proceed {
		t.Error("esc must not proceed")
	}
}

// With a scope flag (toggles off), w/b/a are inert — the scope is fixed.
func TestPruneConfirm_NoTogglesIgnoresKeys(t *testing.T) {
	m, previewed := newConfirmModel(false)
	m.scope = scopeBranches
	for _, k := range []string{"w", "b", "a"} {
		m, _ = sendPrune(m, runes(k))
	}
	if m.scope != scopeBranches {
		t.Errorf("scope changed to %v with toggles off", m.scope)
	}
	if len(*previewed) != 0 {
		t.Errorf("re-previewed %v with toggles off; want none", *previewed)
	}
}
