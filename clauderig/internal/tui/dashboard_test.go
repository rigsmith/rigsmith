package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rigsmith/clauderig/internal/config"
	"github.com/rigsmith/clauderig/internal/status"
)

func keyMsg(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func TestDashboard_HotkeysChooseAction(t *testing.T) {
	// A fully set-up state: remote + a snapshot, so every action applies.
	base := New(status.Info{Machine: config.Machine{Name: "mbp", OS: "macos"}, Remote: "git@x", LastSync: "abc"})
	for key, want := range map[string]string{"i": "init", "s": "sync", "r": "restore", "t": "status"} {
		m, _ := base.Update(keyMsg(key))
		if got := m.(Model).Chosen; got != want {
			t.Errorf("key %q → Chosen %q, want %q", key, got, want)
		}
	}
}

// sync/restore are gated by state — pressing them does nothing (and they don't
// appear in the legend) when the workspace can't support them.
func TestDashboard_GatedActions(t *testing.T) {
	noRemote := New(status.Info{}) // no remote, never synced
	if m, _ := noRemote.Update(keyMsg("s")); m.(Model).Chosen != "" {
		t.Error("sync should be inert with no remote")
	}
	if m, _ := noRemote.Update(keyMsg("r")); m.(Model).Chosen != "" {
		t.Error("restore should be inert with no snapshot")
	}
	// "restore" appears only in the legend/next-step, so its absence proves the
	// action is hidden. (sync gating is covered by the inert-key check above; the
	// word "sync" also appears in the "last sync" status line.)
	if view := noRemote.View(); strings.Contains(view, "restore") {
		t.Errorf("legend should hide restore when unavailable\n%s", view)
	}
	// init and status remain available regardless of state.
	if m, _ := noRemote.Update(keyMsg("i")); m.(Model).Chosen != "init" {
		t.Error("init should always be available")
	}
	if m, _ := noRemote.Update(keyMsg("t")); m.(Model).Chosen != "status" {
		t.Error("status should always be available")
	}

	// With a remote but no snapshot: sync available, restore not.
	synced := New(status.Info{Remote: "git@x"})
	if m, _ := synced.Update(keyMsg("s")); m.(Model).Chosen != "sync" {
		t.Error("sync should be available with a remote")
	}
	if m, _ := synced.Update(keyMsg("r")); m.(Model).Chosen != "" {
		t.Error("restore should be inert with no snapshot")
	}
}

func TestDashboard_NextStep(t *testing.T) {
	cases := []struct {
		name string
		info status.Info
		has  string
	}{
		{"no remote → init", status.Info{}, "init"},
		{"never synced → sync", status.Info{Remote: "git@x"}, "snapshot"},
		{"dirty → sync", status.Info{Remote: "git@x", LastSync: "abc", Dirty: true}, "not pushed"},
		{"clean → restore", status.Info{Remote: "git@x", LastSync: "abc"}, "restore"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := New(tc.info).nextStep(); !strings.Contains(got, tc.has) {
				t.Errorf("nextStep = %q, want substring %q", got, tc.has)
			}
		})
	}
}

func TestDashboard_QuitChoosesNothing(t *testing.T) {
	base := New(status.Info{})
	m, cmd := base.Update(keyMsg("q"))
	if m.(Model).Chosen != "" {
		t.Errorf("quit should choose nothing, got %q", m.(Model).Chosen)
	}
	if cmd == nil {
		t.Error("quit should return a command (tea.Quit)")
	}
}

func TestDashboard_ViewRendersState(t *testing.T) {
	info := status.Info{
		Machine:  config.Machine{Name: "mbp", OS: "macos"},
		Remote:   "git@github.com:john/x.git",
		LastSync: "abc123 2h ago — sync",
		Roots:    []status.RootInfo{{ID: "cli", Files: 42, Present: true}, {ID: "desktop", Present: false}},
		Hooks:    []string{"Stop"},
	}
	view := New(info).View()
	for _, want := range []string{"clauderig", "mbp", "git@github.com:john/x.git", "42 files", "absent here", "sync", "restore"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q\n%s", want, view)
		}
	}
}
