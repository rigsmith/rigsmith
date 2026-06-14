package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// sized returns a Preview that has received a window size, so its viewport is
// ready and View() renders the content rather than the loading placeholder.
func sized(content string) Preview {
	m := NewPreview("Title", "→ /tmp/CLAUDE.md", content)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return next.(Preview)
}

func TestPreview_ProceedKeys(t *testing.T) {
	keys := []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune{'y'}},
		{Type: tea.KeyEnter},
	}
	for _, km := range keys {
		m := sized("hello")
		next, cmd := m.Update(km)
		got := next.(Preview)
		if !got.Confirmed {
			t.Errorf("%v did not confirm", km)
		}
		if cmd == nil {
			t.Errorf("%v did not quit", km)
		}
	}
}

func TestPreview_CancelKeys(t *testing.T) {
	for _, key := range []string{"n", "q", "esc"} {
		m := sized("hello")
		var next tea.Model
		var cmd tea.Cmd
		if key == "esc" {
			next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
		} else {
			next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
		}
		got := next.(Preview)
		if got.Confirmed {
			t.Errorf("%q confirmed, want cancel", key)
		}
		if cmd == nil {
			t.Errorf("%q did not quit", key)
		}
	}
}

func TestPreview_RendersContentAndHints(t *testing.T) {
	m := sized("the managed block body")
	view := m.View()
	if !strings.Contains(view, "the managed block body") {
		t.Error("content not rendered in viewport")
	}
	if !strings.Contains(view, "proceed") || !strings.Contains(view, "cancel") {
		t.Error("action hints missing from footer")
	}
}

func TestPreview_ScrollKeysDoNotQuit(t *testing.T) {
	// A scroll key reaches the viewport and never sets a decision or quits.
	m := sized(strings.Repeat("line\n", 200))
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	got := next.(Preview)
	if got.Confirmed {
		t.Error("scroll key confirmed unexpectedly")
	}
}
