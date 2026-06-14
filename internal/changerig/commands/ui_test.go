package commands

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// labels extracts menu item labels for assertions.
func labels(items []menuItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.label
	}
	return out
}

func has(items []menuItem, label string) bool {
	for _, it := range items {
		if it.label == label {
			return true
		}
	}
	return false
}

// buildItems is state-driven: it shows only the verbs that apply to the current
// workspace state, so the menu reflects what's actually available.
func TestBuildItems_StateGating(t *testing.T) {
	t.Run("uninitialized → initialize + info only", func(t *testing.T) {
		items := buildItems(wsState{initialized: false}, nil)
		if got := labels(items); len(got) != 2 || got[0] != "Initialize" || got[1] != "Info" {
			t.Fatalf("labels = %v, want [Initialize Info]", got)
		}
	})

	t.Run("changeset mode, no pending → no browse, no version", func(t *testing.T) {
		items := buildItems(wsState{initialized: true, usesChangesets: true, pending: 0}, nil)
		if has(items, "Browse changesets") {
			t.Error("Browse should be hidden with 0 pending changesets")
		}
		if has(items, "Version") {
			t.Error("Version should be hidden with nothing to release")
		}
		for _, want := range []string{"Status", "Add changeset", "Info"} {
			if !has(items, want) {
				t.Errorf("missing %q in %v", want, labels(items))
			}
		}
	})

	t.Run("changeset mode, pending → browse + version appear", func(t *testing.T) {
		items := buildItems(wsState{initialized: true, usesChangesets: true, pending: 3}, nil)
		for _, want := range []string{"Status", "Add changeset", "Browse changesets", "Version", "Info"} {
			if !has(items, want) {
				t.Errorf("missing %q in %v", want, labels(items))
			}
		}
	})

	t.Run("commit mode → no add/browse, version shown", func(t *testing.T) {
		items := buildItems(wsState{initialized: true, usesChangesets: false, usesCommits: true, pending: 0}, nil)
		if has(items, "Add changeset") || has(items, "Browse changesets") {
			t.Error("Add/Browse should be hidden in pure commit mode")
		}
		if !has(items, "Version") {
			t.Error("Version should show in commit mode")
		}
	})

	t.Run("prerelease → exit entry appears", func(t *testing.T) {
		items := buildItems(wsState{initialized: true, usesChangesets: true, pending: 1, inPre: true, preTag: "next"}, nil)
		if !has(items, "Exit prerelease (next)") {
			t.Errorf("missing Exit prerelease entry in %v", labels(items))
		}
	})

	t.Run("tool extras are appended before Info", func(t *testing.T) {
		extra := []MenuItem{{Label: "Publish", Desc: "publish"}, {Label: "Release", Desc: "pipeline"}}
		items := buildItems(wsState{initialized: true, usesChangesets: true, pending: 1}, extra)
		got := labels(items)
		if !has(items, "Publish") || !has(items, "Release") {
			t.Fatalf("extras missing in %v", got)
		}
		if got[len(got)-1] != "Info" {
			t.Errorf("Info should be last, got %v", got)
		}
	})
}

// recommend picks a target that buildItems includes for the same state.
func TestRecommend(t *testing.T) {
	cases := []struct {
		name       string
		st         wsState
		wantTarget string
		hintHas    string
	}{
		{"pending → status", wsState{initialized: true, usesChangesets: true, pending: 2}, "Status", "pending"},
		{"commit-driven → status", wsState{initialized: true, usesCommits: true}, "Status", "Commit-driven"},
		{"nothing pending → add", wsState{initialized: true, usesChangesets: true}, "Add changeset", "Add changeset"},
		{"prerelease → status", wsState{initialized: true, usesChangesets: true, pending: 1, inPre: true, preTag: "next"}, "Status", "Prerelease mode (next)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			target, hint := recommend(tc.st)
			if target != tc.wantTarget {
				t.Errorf("target = %q, want %q", target, tc.wantTarget)
			}
			if !strings.Contains(hint, tc.hintHas) {
				t.Errorf("hint %q missing %q", hint, tc.hintHas)
			}
			// The recommended target must be a real item in the same state.
			items := buildItems(tc.st, nil)
			if !has(items, target) {
				t.Errorf("recommended %q is not an item: %v", target, labels(items))
			}
			if cursor := markRecommended(items, target); !items[cursor].recommended {
				t.Errorf("markRecommended did not flag target %q", target)
			}
		})
	}
}

// Selecting a verb erases the menu (empty view on the quitting frame), so the
// dispatched command's output starts clean instead of below a stale menu.
func TestMenuView_ClearsOnSelect(t *testing.T) {
	m := menuModel{title: "changerig", items: buildItems(wsState{initialized: true, usesChangesets: true}, nil)}
	sel, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if sel.(menuModel).View() != "" {
		t.Error("menu should render empty after a verb is chosen")
	}
	// A plain quit (nothing chosen) keeps the menu rendered.
	q, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if q.(menuModel).View() == "" {
		t.Error("plain quit should keep the menu rendered")
	}
}

// The menu view surfaces the hint line and tags the recommended item with
// "next", so the suggested step is visible at a glance.
func TestMenuView_HintAndNextTag(t *testing.T) {
	items := buildItems(wsState{initialized: true, usesChangesets: true}, nil)
	cursor := markRecommended(items, "Add changeset")
	m := menuModel{
		title:  "changerig",
		header: "/repo  ·  3 package(s)  ·  0 pending changeset(s)",
		hint:   "No pending changesets — record a change with Add changeset.",
		items:  items,
		cursor: cursor,
	}
	view := m.View()
	for _, want := range []string{"changerig", "record a change", "next", "Add changeset"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q\n%s", want, view)
		}
	}
}

// The menu shows the invoking tool's brand banner above it — shiprig's when this
// shared menu is reused there, changerig's otherwise (keyed by tagline).
func TestMenuBanner(t *testing.T) {
	if !strings.Contains(bannerFor("shiprig"), "release front door") {
		t.Error("shiprig banner missing its tagline")
	}
	if !strings.Contains(bannerFor("changerig"), "changeset lifecycle") {
		t.Error("changerig banner missing its tagline")
	}
	m := menuModel{banner: bannerFor("shiprig"), header: "x", items: []menuItem{{label: "Status"}}}
	if !strings.Contains(m.View(), "release front door") {
		t.Errorf("View should render the tool banner above the menu\n%s", m.View())
	}
}
