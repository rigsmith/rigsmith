package cli

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rigsmith/rigsmith/internal/changerig/commands"
)

func key(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

func testPicker(t *testing.T, rps []commands.ReleasePkg, ignore []string) (*pkgPickerModel, *[]string) {
	t.Helper()
	m := newPkgPicker(rps, ignore)
	var wrote []string
	m.write = func(globs []string) (string, bool, error) {
		wrote = append([]string{}, globs...) // snapshot the persisted list
		return "config", true, nil
	}
	return &m, &wrote
}

// drive applies a key and returns the updated model (bubbletea passes models by value).
func drive(m *pkgPickerModel, s string) {
	next, _ := m.Update(key(s))
	*m = next.(pkgPickerModel)
}

func TestPickerExcludeIncludeRoundTrip(t *testing.T) {
	rps := []commands.ReleasePkg{
		{Name: "core", Eco: "go", Current: "1.0.0", Next: "1.1.0", Bump: "minor"},
		{Name: "demo", Eco: "node", Current: "0.1.0", Private: true},
	}
	m, wrote := testPicker(t, rps, nil)

	// Cursor starts on "core"; exclude it.
	drive(m, "x")
	if len(*wrote) != 1 || (*wrote)[0] != "core" {
		t.Fatalf("after exclude, persisted = %v, want [core]", *wrote)
	}
	// With showIgnored off, the excluded row drops out of the visible set.
	if len(m.vis) != 1 || m.vis[0].name != "demo" {
		t.Fatalf("excluded row should hide: visible = %+v", m.vis)
	}

	// Reveal excluded, move to it, re-include.
	drive(m, "a") // show excluded
	// core sorts before demo, so cursor 0 is core again.
	drive(m, "i")
	if len(*wrote) != 0 {
		t.Fatalf("after include, persisted = %v, want []", *wrote)
	}
	for _, r := range m.all {
		if r.name == "core" && r.ignored {
			t.Error("core should be included again")
		}
	}
}

func TestPickerIncludeDropsCoveringGlob(t *testing.T) {
	rps := []commands.ReleasePkg{{Name: "shiprig-tauri-demo", Eco: "tauri", Current: "0.1.0"}}
	// Ignored via a wildcard, not its exact name.
	m, wrote := testPicker(t, rps, []string{"*-demo"})

	if len(m.vis) != 0 {
		t.Fatalf("glob-ignored row should be hidden: %+v", m.vis)
	}
	drive(m, "a") // reveal
	drive(m, "i") // include → must drop the covering "*-demo" glob
	if len(*wrote) != 0 {
		t.Fatalf("including should drop the covering glob, persisted = %v", *wrote)
	}
}

func TestPickerExcludeIsNoOpWhenAlreadyIgnored(t *testing.T) {
	rps := []commands.ReleasePkg{{Name: "core", Eco: "go", Current: "1.0.0"}}
	m, wrote := testPicker(t, rps, []string{"core"})
	drive(m, "a") // reveal the ignored row
	drive(m, "x") // already ignored → no write
	if *wrote != nil {
		t.Fatalf("excluding an already-ignored package should not persist: %v", *wrote)
	}
}
