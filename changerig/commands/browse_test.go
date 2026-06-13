package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rigsmith/core/changeset"
)

func TestChangesetBadge(t *testing.T) {
	cases := []struct {
		name string
		cs   *changeset.Changeset
		want string
	}{
		{"explicit highest bump", &changeset.Changeset{Releases: []changeset.Release{
			{Name: "a", Bump: changeset.BumpPatch}, {Name: "b", Bump: changeset.BumpMinor}}}, "minor"},
		{"derive from type", &changeset.Changeset{
			Releases: []changeset.Release{{Name: "a", Bump: changeset.BumpNone}}, Type: "feat"}, "feat"},
		{"breaking type", &changeset.Changeset{
			Releases: []changeset.Release{{Name: "a"}}, Type: "feat", Breaking: true}, "feat!"},
		{"conventional summary", &changeset.Changeset{
			Releases: []changeset.Release{{Name: "a"}}, Summary: "fix: a bug"}, "fix"},
		{"no signal", &changeset.Changeset{Releases: []changeset.Release{{Name: "a"}}}, "auto"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got, _ := changesetBadge(c.cs); got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestRenderChangesetBody(t *testing.T) {
	cs := &changeset.Changeset{
		Releases: []changeset.Release{{Name: "pkg-a", Bump: changeset.BumpMinor}},
		Type:     "feat",
		Summary:  "feat: add a thing\n\nmore detail",
	}
	out := renderChangesetBody(cs)
	for _, want := range []string{"Releases", "pkg-a", "Type", "feat", "Summary", "add a thing", "more detail"} {
		if !strings.Contains(out, want) {
			t.Fatalf("body missing %q:\n%s", want, out)
		}
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("hello", 10); got != "hello" {
		t.Fatalf("short = %q", got)
	}
	if got := truncate("hello world", 6); got != "hello…" {
		t.Fatalf("long = %q, want hello…", got)
	}
}

func writeChangeset(t *testing.T, dir, id, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBrowseModel_DeleteRemovesFileAndReloads(t *testing.T) {
	dir := t.TempDir()
	writeChangeset(t, dir, "aaa", "---\n\"pkg\": minor\n---\n\nfirst")
	writeChangeset(t, dir, "bbb", "---\n\"pkg\": patch\n---\n\nsecond")
	items, err := loadChangesets(dir)
	if err != nil || len(items) != 2 {
		t.Fatalf("setup: %d items, err %v", len(items), err)
	}

	var m tea.Model = newBrowseModel(dir, items)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	// cursor on "aaa"; delete it (d then y).
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})

	if _, err := os.Stat(filepath.Join(dir, "aaa.md")); !os.IsNotExist(err) {
		t.Fatal("aaa.md should have been deleted")
	}
	bm := m.(browseModel)
	if len(bm.items) != 1 || bm.items[0].ID != "bbb" {
		t.Fatalf("after delete: items = %+v, want only bbb", bm.items)
	}
}

func TestBrowseModel_EnterOpensDetailEscReturns(t *testing.T) {
	dir := t.TempDir()
	writeChangeset(t, dir, "aaa", "---\n\"pkg\": minor\n---\n\nthe summary body")
	items, _ := loadChangesets(dir)

	var m tea.Model = newBrowseModel(dir, items)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !m.(browseModel).detail {
		t.Fatal("enter should open the detail view")
	}
	if !strings.Contains(m.View(), "the summary body") {
		t.Fatalf("detail view missing the body:\n%s", m.View())
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.(browseModel).detail {
		t.Fatal("esc should return to the list")
	}
}
