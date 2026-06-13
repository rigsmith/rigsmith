package commands

import (
	"testing"

	"github.com/rigsmith/clauderig/internal/gitrepo"
)

func TestResolveWorktree(t *testing.T) {
	main := gitrepo.Worktree{Path: "/repo", Branch: "main"}
	gw := gitrepo.Worktree{Path: "/repo-wt/feat-go-watch", Branch: "feat/go-watch"}
	gw2 := gitrepo.Worktree{Path: "/repo-wt/feat-go-watcher", Branch: "feat/go-watcher"}

	t.Run("no query, only main -> main", func(t *testing.T) {
		got, err := resolveWorktree([]gitrepo.Worktree{main}, "")
		if err != nil || got != main.Path {
			t.Fatalf("got %q, %v; want %q", got, err, main.Path)
		}
	})

	t.Run("no query, one linked -> auto-select", func(t *testing.T) {
		got, err := resolveWorktree([]gitrepo.Worktree{main, gw}, "")
		if err != nil || got != gw.Path {
			t.Fatalf("got %q, %v; want %q", got, err, gw.Path)
		}
	})

	t.Run("exact branch query", func(t *testing.T) {
		got, err := resolveWorktree([]gitrepo.Worktree{main, gw, gw2}, "feat/go-watch")
		if err != nil || got != gw.Path {
			t.Fatalf("got %q, %v; want %q", got, err, gw.Path)
		}
	})

	t.Run("fuzzy query picks best match", func(t *testing.T) {
		got, err := resolveWorktree([]gitrepo.Worktree{main, gw, gw2}, "gowatch")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Both go-watch* match; the shorter branch wins the tie.
		if got != gw.Path {
			t.Fatalf("got %q; want %q", got, gw.Path)
		}
	})

	t.Run("no match errors", func(t *testing.T) {
		if _, err := resolveWorktree([]gitrepo.Worktree{main, gw}, "zzz"); err == nil {
			t.Fatal("expected error for non-matching query")
		}
	})
}

func TestWorktreeCompletions(t *testing.T) {
	main := gitrepo.Worktree{Path: "/repo", Branch: "main"}
	gw := gitrepo.Worktree{Path: "/repo-wt/feat-go-watch", Branch: "feat/go-watch"}
	detached := gitrepo.Worktree{Path: "/repo-wt/detached", Branch: ""}

	t.Run("branch and short name, each described by path", func(t *testing.T) {
		got := worktreeCompletions([]gitrepo.Worktree{main, gw})
		want := []string{
			"main\t/repo",
			"feat/go-watch\t/repo-wt/feat-go-watch",
			"go-watch\t/repo-wt/feat-go-watch",
		}
		assertEqual(t, got, want)
	})

	t.Run("no short-name dup when it equals the branch", func(t *testing.T) {
		// "main" has no prefix to strip, so it must appear exactly once.
		got := worktreeCompletions([]gitrepo.Worktree{main})
		assertEqual(t, got, []string{"main\t/repo"})
	})

	t.Run("detached worktree contributes nothing", func(t *testing.T) {
		got := worktreeCompletions([]gitrepo.Worktree{detached})
		assertEqual(t, got, nil)
	})
}

func assertEqual(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %v; want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("at %d got %q; want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}
