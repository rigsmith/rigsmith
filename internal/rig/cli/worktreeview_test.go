package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/core/gitrepo"
)

func TestHumanizeAgo(t *testing.T) {
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		t    time.Time
		want string
	}{
		{"zero is empty", time.Time{}, ""},
		{"future clamps to just now", now.Add(time.Hour), "just now"},
		{"seconds", now.Add(-30 * time.Second), "just now"},
		{"minutes", now.Add(-5 * time.Minute), "5m ago"},
		{"hours", now.Add(-3 * time.Hour), "3h ago"},
		{"days", now.Add(-2 * 24 * time.Hour), "2d ago"},
		{"weeks", now.Add(-14 * 24 * time.Hour), "2w ago"},
		{"years", now.Add(-400 * 24 * time.Hour), "1y ago"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := humanizeAgo(c.t, now); got != c.want {
				t.Fatalf("humanizeAgo = %q; want %q", got, c.want)
			}
		})
	}
}

func TestWorktreesByRecent(t *testing.T) {
	base := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	old := gitrepo.Worktree{Path: "/old", Branch: "old", ModTime: base.Add(-48 * time.Hour)}
	mid := gitrepo.Worktree{Path: "/mid", Branch: "mid", ModTime: base.Add(-1 * time.Hour)}
	zeroA := gitrepo.Worktree{Path: "/za", Branch: "za"} // unknown time
	zeroB := gitrepo.Worktree{Path: "/zb", Branch: "zb"} // unknown time
	in := []gitrepo.Worktree{old, zeroA, mid, zeroB}

	got := worktreesByRecent(in)

	order := []string{got[0].Path, got[1].Path, got[2].Path, got[3].Path}
	// Newest first, then the zero-time entries last in their original order.
	want := []string{"/mid", "/old", "/za", "/zb"}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v; want %v", order, want)
		}
	}
	// The input slice must be left untouched — callers rely on git's ordering.
	if in[0].Path != "/old" || in[1].Path != "/za" {
		t.Fatalf("worktreesByRecent mutated its input: %v", []string{in[0].Path, in[1].Path})
	}
}

// The -wt menu shows each worktree's age, sorts newest-first, and parks the
// cursor on the pinned tree even after the sort.
func TestWtMenuShowsAgeAndSorts(t *testing.T) {
	now := time.Now()
	stale := gitrepo.Worktree{Path: "/r/stale", Branch: "stale", ModTime: now.Add(-72 * time.Hour)}
	fresh := gitrepo.Worktree{Path: "/r/fresh", Branch: "fresh", ModTime: now.Add(-2 * time.Hour)}
	wts := []gitrepo.Worktree{stale, fresh}

	m := newWtMenu(wts, stale.Path)
	if m.wts[0].Path != fresh.Path {
		t.Fatalf("newest worktree not first: %q", m.wts[0].Path)
	}
	if m.cursor != 1 || m.wts[m.cursor].Path != stale.Path {
		t.Fatalf("cursor=%d on %q; want it parked on the pinned %q", m.cursor, m.wts[m.cursor].Path, stale.Path)
	}
	plain := wtAnsiRE.ReplaceAllString(m.View(), "")
	if !strings.Contains(plain, "2h ago") || !strings.Contains(plain, "3d ago") {
		t.Fatalf("view missing age column:\n%s", plain)
	}
}
