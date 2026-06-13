package gitrepo

import (
	"context"
	"path/filepath"
	"testing"
)

// commitFile writes rel and commits it on the current branch, returning nothing.
func commitFile(t *testing.T, r *Repo, rel, content, msg string) {
	t.Helper()
	ctx := context.Background()
	write(t, r.Dir, rel, content)
	if _, err := runGit(ctx, r.Dir, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, r.Dir, "-c", "commit.gpgsign=false", "commit", "-m", msg); err != nil {
		t.Fatal(err)
	}
}

func TestIsMergedAncestor(t *testing.T) {
	ctx := context.Background()
	r, _ := Init(ctx, t.TempDir())
	commitFile(t, r, "a", "1", "init")

	// feature branched and never merged.
	if err := r.Checkout(ctx, "feature", true); err != nil {
		t.Fatal(err)
	}
	commitFile(t, r, "b", "2", "feature work")

	if merged, err := r.IsMerged(ctx, "feature", "main"); err != nil || merged {
		t.Fatalf("unmerged feature: merged=%v err=%v", merged, err)
	}

	// Fast-forward main to feature — now it's an ancestor.
	if err := r.Checkout(ctx, "main", false); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, r.Dir, "merge", "--ff-only", "feature"); err != nil {
		t.Fatal(err)
	}
	if merged, err := r.IsMerged(ctx, "feature", "main"); err != nil || !merged {
		t.Fatalf("ff-merged feature: merged=%v err=%v", merged, err)
	}
}

// The case that matters here: a squash-merge leaves the branch tip off main's
// history, so a plain ancestor test fails — IsMerged must still see it merged.
func TestIsMergedSquash(t *testing.T) {
	ctx := context.Background()
	r, _ := Init(ctx, t.TempDir())
	commitFile(t, r, "a", "1", "init")

	if err := r.Checkout(ctx, "feature", true); err != nil {
		t.Fatal(err)
	}
	commitFile(t, r, "b", "2\n", "feature 1")
	commitFile(t, r, "c", "3\n", "feature 2")

	// Squash-merge: stage feature's net change onto main as one new commit.
	if err := r.Checkout(ctx, "main", false); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, r.Dir, "merge", "--squash", "feature"); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, r.Dir, "-c", "commit.gpgsign=false", "commit", "-m", "squash feature (#1)"); err != nil {
		t.Fatal(err)
	}

	// Branch tip is NOT an ancestor of main...
	if code, err := gitExitCode(ctx, r.Dir, "merge-base", "--is-ancestor", "feature", "main"); err != nil || code == 0 {
		t.Fatalf("sanity: expected non-ancestor, code=%d err=%v", code, err)
	}
	// ...but IsMerged should detect the equivalent patch.
	if merged, err := r.IsMerged(ctx, "feature", "main"); err != nil || !merged {
		t.Fatalf("squash-merged feature: merged=%v err=%v", merged, err)
	}
}

// A branch with extra unmerged work on top of a squash-merge must read unmerged.
func TestIsMergedSquashThenMoreWork(t *testing.T) {
	ctx := context.Background()
	r, _ := Init(ctx, t.TempDir())
	commitFile(t, r, "a", "1", "init")

	r.Checkout(ctx, "feature", true)
	commitFile(t, r, "b", "2\n", "feature 1")

	r.Checkout(ctx, "main", false)
	runGit(ctx, r.Dir, "merge", "--squash", "feature")
	runGit(ctx, r.Dir, "-c", "commit.gpgsign=false", "commit", "-m", "squash feature (#1)")

	// New work lands on feature after the squash.
	r.Checkout(ctx, "feature", false)
	commitFile(t, r, "d", "4\n", "more feature work")

	if merged, err := r.IsMerged(ctx, "feature", "main"); err != nil || merged {
		t.Fatalf("feature with new work: merged=%v err=%v", merged, err)
	}
}

func TestWorktreeCleanAndPrune(t *testing.T) {
	ctx := context.Background()
	r, _ := Init(ctx, t.TempDir())
	commitFile(t, r, "a", "1", "init")

	wtPath := filepath.Join(t.TempDir(), "wt")
	if err := r.WorktreeAdd(ctx, wtPath, "feature", "main", true); err != nil {
		t.Fatal(err)
	}

	if clean, err := r.WorktreeClean(ctx, wtPath); err != nil || !clean {
		t.Fatalf("fresh worktree clean: clean=%v err=%v", clean, err)
	}
	write(t, wtPath, "dirty", "x")
	if clean, err := r.WorktreeClean(ctx, wtPath); err != nil || clean {
		t.Fatalf("dirty worktree clean: clean=%v err=%v", clean, err)
	}

	if err := r.WorktreeRemove(ctx, wtPath, true); err != nil {
		t.Fatal(err)
	}
	if err := r.WorktreePruneMeta(ctx); err != nil {
		t.Fatalf("prune meta: %v", err)
	}
}
