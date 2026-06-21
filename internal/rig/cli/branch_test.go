package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/rigsmith/rigsmith/core/gitrepo"
)

// commit writes rel and commits it on the current branch.
func commit(t *testing.T, r *gitrepo.Repo, rel, content, msg string) {
	t.Helper()
	ctx := context.Background()
	if err := os.WriteFile(filepath.Join(r.Dir, rel), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.StageAll(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Commit(ctx, msg); err != nil {
		t.Fatal(err)
	}
}

// ffMerge fast-forwards the current branch onto branch — modelling work that
// landed in base with no merge commit, leaving base's tip even with branch's.
func ffMerge(t *testing.T, r *gitrepo.Repo, branch string) {
	t.Helper()
	if out, err := exec.Command("git", "-C", r.Dir, "merge", "--ff-only", branch).CombinedOutput(); err != nil {
		t.Fatalf("ff merge %s: %v\n%s", branch, err, out)
	}
}

func TestClassifyBranch(t *testing.T) {
	ctx := context.Background()
	r, err := gitrepo.Init(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	commit(t, r, "a", "1", "init")

	// merged: a branch at main's tip is an ancestor of main.
	if err := r.Checkout(ctx, "merged", true); err != nil {
		t.Fatal(err)
	}
	// unmerged: a branch with its own commit not in main.
	if err := r.Checkout(ctx, "work", true); err != nil {
		t.Fatal(err)
	}
	commit(t, r, "b", "2", "work")
	if err := r.Checkout(ctx, "main", false); err != nil {
		t.Fatal(err)
	}

	// attached: a branch checked out in a worktree.
	wtPath := filepath.Join(t.TempDir(), "wt")
	if err := r.WorktreeAdd(ctx, wtPath, "attached", "main", true); err != nil {
		t.Fatal(err)
	}
	inWorktree := worktreeBranches(ctx, r)

	branches, err := r.LocalBranches(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, b := range branches {
		got[b.Name] = classifyBranch(ctx, r, b, "main", inWorktree).tag
	}

	want := map[string]string{
		"main":     "current",
		"merged":   "merged",
		"work":     "unmerged",
		"attached": "worktree",
	}
	for name, tag := range want {
		if got[name] != tag {
			t.Errorf("classify %q = %q, want %q", name, got[name], tag)
		}
	}
}
