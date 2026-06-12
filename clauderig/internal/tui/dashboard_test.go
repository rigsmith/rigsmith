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
	base := New(status.Info{Machine: config.Machine{Name: "mbp", OS: "macos"}})
	for key, want := range map[string]string{"s": "sync", "r": "restore", "t": "status"} {
		m, _ := base.Update(keyMsg(key))
		if got := m.(Model).Chosen; got != want {
			t.Errorf("key %q → Chosen %q, want %q", key, got, want)
		}
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
