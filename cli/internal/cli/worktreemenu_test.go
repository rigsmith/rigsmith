package cli

import (
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/rigsmith/core/gitrepo"
)

var wtAnsiRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func wtKeyMsg(s string) tea.KeyMsg {
	if s == "enter" {
		return tea.KeyMsg{Type: tea.KeyEnter}
	}
	if s == "up" {
		return tea.KeyMsg{Type: tea.KeyUp}
	}
	if s == "down" {
		return tea.KeyMsg{Type: tea.KeyDown}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func wtSend(m wtMenuModel, k string) wtMenuModel {
	next, _ := m.Update(wtKeyMsg(k))
	return next.(wtMenuModel)
}

func TestWtMenuModel(t *testing.T) {
	main := gitrepo.Worktree{Path: "/repo", Branch: "main"}
	a := gitrepo.Worktree{Path: "/repo-wt/a", Branch: "feat/a"}
	b := gitrepo.Worktree{Path: "/repo-wt/b", Branch: "feat/b"}
	wts := []gitrepo.Worktree{main, a, b}

	t.Run("cursor starts on the pinned worktree", func(t *testing.T) {
		m := newWtMenu(wts, b.Path)
		if m.cursor != 2 {
			t.Fatalf("cursor = %d; want 2 (the pinned worktree)", m.cursor)
		}
	})

	t.Run("enter runs the selected worktree", func(t *testing.T) {
		m := wtSend(newWtMenu(wts, ""), "down") // main -> feat/a
		m = wtSend(m, "enter")
		if m.action != wtRun || m.chosen != a.Path {
			t.Fatalf("action=%v chosen=%q; want run %q", m.action, m.chosen, a.Path)
		}
	})

	t.Run("p pins the selected worktree", func(t *testing.T) {
		m := wtSend(newWtMenu(wts, ""), "down")
		m = wtSend(m, "down") // feat/b
		m = wtSend(m, "p")
		if m.action != wtPin || m.chosen != b.Path {
			t.Fatalf("action=%v chosen=%q; want pin %q", m.action, m.chosen, b.Path)
		}
	})

	t.Run("u unpins", func(t *testing.T) {
		m := wtSend(newWtMenu(wts, a.Path), "u")
		if m.action != wtUnpin {
			t.Fatalf("action=%v; want unpin", m.action)
		}
	})

	t.Run("q cancels", func(t *testing.T) {
		m := wtSend(newWtMenu(wts, ""), "q")
		if m.action != wtCancel {
			t.Fatalf("action=%v; want cancel", m.action)
		}
	})

	t.Run("cursor clamps at the ends", func(t *testing.T) {
		m := wtSend(newWtMenu(wts, ""), "up") // already at top
		if m.cursor != 0 {
			t.Fatalf("cursor = %d; want 0", m.cursor)
		}
		for i := 0; i < 10; i++ {
			m = wtSend(m, "down")
		}
		if m.cursor != len(wts)-1 {
			t.Fatalf("cursor = %d; want %d", m.cursor, len(wts)-1)
		}
	})

	t.Run("view marks the pinned worktree", func(t *testing.T) {
		out := newWtMenu(wts, b.Path).View()
		if !strings.Contains(out, "pinned") {
			t.Fatalf("view missing pinned marker:\n%s", out)
		}
	})
}

// The path column lines up regardless of branch width or which row holds the
// (multi-byte) cursor — the "columns" half of the -wt menu polish.
func TestWtMenuColumnsAlign(t *testing.T) {
	wts := []gitrepo.Worktree{
		{Path: "/r", Branch: "main"},
		{Path: "/r-wt/feat-a", Branch: "feat/a"},
		{Path: "/r-wt/much-longer-branch", Branch: "feat/much-longer-branch"},
	}
	plain := wtAnsiRE.ReplaceAllString(newWtMenu(wts, wts[1].Path).View(), "")
	col := -1
	for _, wt := range wts {
		for _, line := range strings.Split(plain, "\n") {
			idx := strings.Index(line, wt.Path)
			if idx < 0 {
				continue
			}
			// Compare display columns: the cursor glyph (▸) is multi-byte, so a raw
			// byte index would differ between the cursor row and the rest.
			dispCol := lipgloss.Width(line[:idx])
			if col == -1 {
				col = dispCol
			} else if dispCol != col {
				t.Errorf("path %q starts at column %d, want %d (not aligned)\nline: %q", wt.Path, dispCol, col, line)
			}
			break
		}
	}
}
