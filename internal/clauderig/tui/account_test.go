package tui

import (
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/internal/clauderig/account"
)

// sampleStatuses returns two healthy accounts, marking activeID (if any) live.
func sampleStatuses(activeID string) []account.StoredStatus {
	mk := func(id, email, sub string) account.StoredStatus {
		return account.StoredStatus{
			Account:          account.Account{ID: id, Email: email, SubscriptionType: sub},
			Active:           id == activeID,
			CredentialTokens: true,
			Session:          account.SessionNone,
		}
	}
	return []account.StoredStatus{
		mk("aaaa1111", "work@acme.com", "max"),
		mk("bbbb2222", "me@personal.com", "pro"),
	}
}

func TestAccount_RunTargetsCursor(t *testing.T) {
	// enter runs the selected (first) account as a session.
	m, cmd := NewAccount(sampleStatuses("aaaa1111"), nil, "").Update(keyMsg("enter"))
	act := m.(AccountModel).Action
	if act.Kind != "run" || act.ID != "aaaa1111" {
		t.Fatalf("enter on first row → %+v, want run aaaa1111", act)
	}
	if cmd == nil {
		t.Error("enter should return tea.Quit")
	}
	// r is an accelerator for the same.
	r, _ := NewAccount(sampleStatuses(""), nil, "").Update(keyMsg("r"))
	if r.(AccountModel).Action.Kind != "run" {
		t.Error("r should also run the selected account")
	}
}

func TestAccount_SwitchTargetsCursor(t *testing.T) {
	base := NewAccount(sampleStatuses("aaaa1111"), nil, "")
	down, _ := base.Update(keyMsg("j"))
	sw, _ := down.(AccountModel).Update(keyMsg("s"))
	if act := sw.(AccountModel).Action; act.Kind != "switch" || act.ID != "bbbb2222" {
		t.Fatalf("s on second row → %+v, want switch bbbb2222", act)
	}
}

func TestAccount_RemoveTargetsCursor(t *testing.T) {
	m, _ := NewAccount(sampleStatuses("aaaa1111"), nil, "").Update(keyMsg("x"))
	if act := m.(AccountModel).Action; act.Kind != "remove" || act.ID != "aaaa1111" {
		t.Fatalf("x on first row → %+v, want remove aaaa1111", act)
	}
}

func TestAccount_AddHotkey(t *testing.T) {
	m, _ := NewAccount(sampleStatuses(""), nil, "").Update(keyMsg("a"))
	if got := m.(AccountModel).Action; got.Kind != "add" {
		t.Fatalf("a → %+v, want Kind add", got)
	}
}

// f recaptures only a repairable row: dead stored credential + live session.
func TestAccount_RepairHotkey(t *testing.T) {
	statuses := sampleStatuses("")
	statuses[0].CredentialTokens = false
	statuses[0].Session = account.SessionOK

	m, cmd := NewAccount(statuses, nil, "").Update(keyMsg("f"))
	if act := m.(AccountModel).Action; act.Kind != "repair" || act.ID != "aaaa1111" {
		t.Fatalf("f on repairable row → %+v, want repair aaaa1111", act)
	}
	if cmd == nil {
		t.Error("f should return tea.Quit")
	}

	// Inert on a healthy row…
	down, _ := NewAccount(statuses, nil, "").Update(keyMsg("j"))
	h, _ := down.(AccountModel).Update(keyMsg("f"))
	if h.(AccountModel).Action.Kind != "" {
		t.Error("f should be inert on a healthy account")
	}
	// …and on a dead row with no session to recapture from.
	statuses[0].Session = account.SessionNone
	d, _ := NewAccount(statuses, nil, "").Update(keyMsg("f"))
	if d.(AccountModel).Action.Kind != "" {
		t.Error("f should be inert when there is no session to recapture from")
	}
}

// run/switch are inert on an empty list; add and quit still work.
func TestAccount_EmptyListInertActions(t *testing.T) {
	empty := NewAccount(nil, nil, "")
	if m, _ := empty.Update(keyMsg("enter")); m.(AccountModel).Action.Kind != "" {
		t.Error("run should be inert with no accounts")
	}
	if m, _ := empty.Update(keyMsg("s")); m.(AccountModel).Action.Kind != "" {
		t.Error("switch should be inert with no accounts")
	}
	if m, _ := empty.Update(keyMsg("x")); m.(AccountModel).Action.Kind != "" {
		t.Error("remove should be inert with no accounts")
	}
	if m, _ := empty.Update(keyMsg("f")); m.(AccountModel).Action.Kind != "" {
		t.Error("repair should be inert with no accounts")
	}
	if m, _ := empty.Update(keyMsg("a")); m.(AccountModel).Action.Kind != "add" {
		t.Error("add should still work with no accounts")
	}
}

func TestAccount_QuitBacksOut(t *testing.T) {
	m, cmd := NewAccount(sampleStatuses(""), nil, "").Update(keyMsg("q"))
	if m.(AccountModel).Action.Kind != "" {
		t.Error("q should record no action")
	}
	if cmd == nil {
		t.Error("q should return tea.Quit")
	}
}

func TestAccount_ViewRendersAccountsAndLiveMarker(t *testing.T) {
	view := NewAccount(sampleStatuses("bbbb2222"), nil, "switched to personal").View()
	for _, want := range []string{"accounts", "work@acme.com", "me@personal.com", "max", "switched to personal", "→"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q\n%s", want, view)
		}
	}
	// Healthy accounts show no health warnings and no repair key.
	for _, absent := range []string{"no tokens", "f repair"} {
		if strings.Contains(view, absent) {
			t.Errorf("healthy view should not contain %q\n%s", absent, view)
		}
	}
}

// A dead stored credential is flagged on its row; the repair hint and hotkey
// only appear when a session exists to recapture from.
func TestAccount_ViewRendersHealth(t *testing.T) {
	statuses := sampleStatuses("")
	statuses[0].CredentialTokens = false
	statuses[0].Session = account.SessionOK
	view := NewAccount(statuses, nil, "").View()
	for _, want := range []string{"✗ no tokens", "session ✓", "press f", "f repair"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q\n%s", want, view)
		}
	}

	statuses[0].Session = account.SessionNone
	stuck := NewAccount(statuses, nil, "").View()
	if strings.Contains(stuck, "f repair") {
		t.Errorf("no session to recapture from → no repair key\n%s", stuck)
	}
	if !strings.Contains(stuck, "log in as that account") {
		t.Errorf("stuck account should point at a live re-login\n%s", stuck)
	}
}

func TestAccount_ViewEmptyState(t *testing.T) {
	if v := NewAccount(nil, nil, "").View(); !strings.Contains(v, "no accounts yet") {
		t.Errorf("empty view should prompt to add\n%s", v)
	}
}

func TestAccount_LiveProcsBannerAndToggle(t *testing.T) {
	procs := []account.Instance{{PID: 123, Kind: "claude-vscode"}}
	m := NewAccount(sampleStatuses("aaaa1111"), procs, "")
	v := m.View()
	if !strings.Contains(v, "1 Claude Code process") {
		t.Errorf("view should warn about live processes\n%s", v)
	}
	if strings.Contains(v, "pid 123") {
		t.Error("process list should be hidden until toggled")
	}
	tog, _ := m.Update(keyMsg("p"))
	if !strings.Contains(tog.(AccountModel).View(), "pid 123") {
		t.Error("p should reveal the process list")
	}
}

func TestAccount_ClearsOnAction(t *testing.T) {
	m, _ := NewAccount(sampleStatuses(""), nil, "").Update(keyMsg("a"))
	if m.(AccountModel).View() != "" {
		t.Error("screen should render empty after an action is chosen")
	}
}
