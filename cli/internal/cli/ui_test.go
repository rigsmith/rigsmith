// Port of the .NET rig's MenuInputTests (the menu's Esc/Backspace cancel keys,
// parity with the Node menu), mapped onto the bubbletea menuModel: in Go the
// cancel keys live in menuModel.Update rather than a wrapped console input, so
// the tests drive Update with scripted key messages — no TTY needed. The .NET
// suite's separate async-read case has no analogue here (bubbletea has a
// single synchronous Update path), so it is covered by the same tests.
package cli

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// scriptedMenu is a two-level menu with deterministic items (newMenu probes
// the cwd's ecosystems, which a unit test must not depend on).
func scriptedMenu() menuModel {
	return menuModel{
		stack: []frame{{items: []menuItem{
			{label: "build", verb: "build"},
			{label: "test", verb: "test"},
			{label: "▸ More", children: []menuItem{{label: "kill", verb: "kill"}}},
		}}},
	}
}

func key(t tea.KeyType) tea.KeyMsg { return tea.KeyMsg{Type: t} }

// isQuit reports whether cmd is tea.Quit.
func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

func TestMenu_EscapeCancelsThePrompt(t *testing.T) {
	model, cmd := scriptedMenu().Update(key(tea.KeyEscape))
	if !isQuit(cmd) {
		t.Fatal("esc at the top level must quit the menu")
	}
	if chosen := model.(menuModel).chosen; chosen != "" {
		t.Fatalf("chosen = %q, want empty (cancelled, nothing runs)", chosen)
	}
}

func TestMenu_BackspaceCancelsThePrompt(t *testing.T) {
	model, cmd := scriptedMenu().Update(key(tea.KeyBackspace))
	if !isQuit(cmd) {
		t.Fatal("backspace at the top level must quit the menu")
	}
	if chosen := model.(menuModel).chosen; chosen != "" {
		t.Fatalf("chosen = %q, want empty", chosen)
	}
}

func TestMenu_EscapePopsASubmenuBeforeCancelling(t *testing.T) {
	m := scriptedMenu()
	// Navigate into the "▸ More" group: down ×2, enter.
	var model tea.Model = m
	for _, k := range []tea.KeyType{tea.KeyDown, tea.KeyDown, tea.KeyEnter} {
		model, _ = model.(menuModel).Update(key(k))
	}
	if depth := len(model.(menuModel).stack); depth != 2 {
		t.Fatalf("stack depth = %d, want 2 (inside the submenu)", depth)
	}

	// Esc pops back to the top level instead of quitting…
	model, cmd := model.(menuModel).Update(key(tea.KeyEscape))
	if isQuit(cmd) {
		t.Fatal("esc inside a submenu must go back, not quit")
	}
	if depth := len(model.(menuModel).stack); depth != 1 {
		t.Fatalf("stack depth = %d, want 1 after esc", depth)
	}
	// …and a second esc cancels for real.
	if _, cmd = model.(menuModel).Update(key(tea.KeyEscape)); !isQuit(cmd) {
		t.Fatal("esc at the top level must quit")
	}
}

func TestMenu_OtherKeysPassThroughUntouched(t *testing.T) {
	// Down moves the cursor without quitting; enter then selects that verb.
	model, cmd := scriptedMenu().Update(key(tea.KeyDown))
	if cmd != nil {
		t.Fatal("a movement key must not produce a command")
	}
	if cursor := model.(menuModel).stack[0].cursor; cursor != 1 {
		t.Fatalf("cursor = %d, want 1", cursor)
	}

	model, cmd = model.(menuModel).Update(key(tea.KeyEnter))
	if !isQuit(cmd) {
		t.Fatal("enter on an action must quit the menu")
	}
	if chosen := model.(menuModel).chosen; chosen != "test" {
		t.Fatalf("chosen = %q, want test", chosen)
	}
}
