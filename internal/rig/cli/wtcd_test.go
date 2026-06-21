package cli

import (
	"testing"

	"github.com/rigsmith/rigsmith/core/gitrepo"
)

// rankWorktrees backs both resolveWorktree (best match) and `wt cd` (which keeps
// the whole match set to disambiguate with a picker), so it must return every
// match, best first — not just the winner.
func TestRankWorktrees(t *testing.T) {
	main := gitrepo.Worktree{Path: "/repo", Branch: "main"}
	gw := gitrepo.Worktree{Path: "/repo-wt/feat-go-watch", Branch: "feat/go-watch"}
	gw2 := gitrepo.Worktree{Path: "/repo-wt/feat-go-watcher", Branch: "feat/go-watcher"}
	wts := []gitrepo.Worktree{main, gw, gw2}

	t.Run("a uniquely-matching query is the lone match", func(t *testing.T) {
		got := rankWorktrees(wts, "watcher")
		if len(got) != 1 || got[0].Path != gw2.Path {
			t.Fatalf("got %+v; want exactly %q", got, gw2.Path)
		}
	})

	t.Run("an ambiguous query keeps every match, exact/shortest first", func(t *testing.T) {
		// "feat/go-watch" is exact for gw but also a prefix of gw2's branch, so
		// both match — like `rig cd`, `wt cd` then disambiguates with a picker.
		got := rankWorktrees(wts, "feat/go-watch")
		if len(got) != 2 {
			t.Fatalf("got %d matches, want 2: %+v", len(got), got)
		}
		if got[0].Path != gw.Path {
			t.Fatalf("best match = %q; want the exact/shorter branch %q", got[0].Path, gw.Path)
		}
	})

	t.Run("no match -> empty", func(t *testing.T) {
		if got := rankWorktrees(wts, "zzz"); len(got) != 0 {
			t.Fatalf("got %+v; want no matches", got)
		}
	})
}

func TestWorktreeBranchLabel(t *testing.T) {
	if got := worktreeBranchLabel(gitrepo.Worktree{Branch: "feat/x"}); got != "feat/x" {
		t.Errorf("got %q; want the branch name", got)
	}
	if got := worktreeBranchLabel(gitrepo.Worktree{Branch: ""}); got != "(detached)" {
		t.Errorf("got %q; want (detached) for an empty branch", got)
	}
}
