package tui

import (
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/internal/clauderig/account"
)

func sampleAccounts() []account.Account {
	return []account.Account{
		{ID: "aaaa1111", Label: "work", SubscriptionType: "max"},
		{ID: "bbbb2222", Label: "personal", SubscriptionType: "pro"},
	}
}

func TestAccount_RunTargetsCursor(t *testing.T) {
	// enter runs the selected (first) account as a session.
	m, cmd := NewAccount(sampleAccounts(), "aaaa1111", "").Update(keyMsg("enter"))
	act := m.(AccountModel).Action
	if act.Kind != "run" || act.ID != "aaaa1111" {
		t.Fatalf("enter on first row → %+v, want run aaaa1111", act)
	}
	if cmd == nil {
		t.Error("enter should return tea.Quit")
	}
	// r is an accelerator for the same.
	r, _ := NewAccount(sampleAccounts(), "", "").Update(keyMsg("r"))
	if r.(AccountModel).Action.Kind != "run" {
		t.Error("r should also run the selected account")
	}
}

func TestAccount_SwitchTargetsCursor(t *testing.T) {
	base := NewAccount(sampleAccounts(), "aaaa1111", "")
	down, _ := base.Update(keyMsg("j"))
	sw, _ := down.(AccountModel).Update(keyMsg("s"))
	if act := sw.(AccountModel).Action; act.Kind != "switch" || act.ID != "bbbb2222" {
		t.Fatalf("s on second row → %+v, want switch bbbb2222", act)
	}
}

func TestAccount_RemoveTargetsCursor(t *testing.T) {
	m, _ := NewAccount(sampleAccounts(), "aaaa1111", "").Update(keyMsg("x"))
	if act := m.(AccountModel).Action; act.Kind != "remove" || act.ID != "aaaa1111" {
		t.Fatalf("x on first row → %+v, want remove aaaa1111", act)
	}
}

func TestAccount_AddHotkey(t *testing.T) {
	m, _ := NewAccount(sampleAccounts(), "", "").Update(keyMsg("a"))
	if got := m.(AccountModel).Action; got.Kind != "add" {
		t.Fatalf("a → %+v, want Kind add", got)
	}
}

// run/switch are inert on an empty list; add and quit still work.
func TestAccount_EmptyListInertActions(t *testing.T) {
	empty := NewAccount(nil, "", "")
	if m, _ := empty.Update(keyMsg("enter")); m.(AccountModel).Action.Kind != "" {
		t.Error("run should be inert with no accounts")
	}
	if m, _ := empty.Update(keyMsg("s")); m.(AccountModel).Action.Kind != "" {
		t.Error("switch should be inert with no accounts")
	}
	if m, _ := empty.Update(keyMsg("x")); m.(AccountModel).Action.Kind != "" {
		t.Error("remove should be inert with no accounts")
	}
	if m, _ := empty.Update(keyMsg("a")); m.(AccountModel).Action.Kind != "add" {
		t.Error("add should still work with no accounts")
	}
}

func TestAccount_QuitBacksOut(t *testing.T) {
	m, cmd := NewAccount(sampleAccounts(), "", "").Update(keyMsg("q"))
	if m.(AccountModel).Action.Kind != "" {
		t.Error("q should record no action")
	}
	if cmd == nil {
		t.Error("q should return tea.Quit")
	}
}

func TestAccount_ViewRendersAccountsAndLiveMarker(t *testing.T) {
	view := NewAccount(sampleAccounts(), "bbbb2222", "switched to personal").View()
	for _, want := range []string{"accounts", "work", "personal", "max", "switched to personal", "→"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q\n%s", want, view)
		}
	}
}

func TestAccount_ViewEmptyState(t *testing.T) {
	if v := NewAccount(nil, "", "").View(); !strings.Contains(v, "no accounts yet") {
		t.Errorf("empty view should prompt to add\n%s", v)
	}
}

func TestAccount_ClearsOnAction(t *testing.T) {
	m, _ := NewAccount(sampleAccounts(), "", "").Update(keyMsg("a"))
	if m.(AccountModel).View() != "" {
		t.Error("screen should render empty after an action is chosen")
	}
}
