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

// BranchAdvanced tells a brand-new branch (only its creation reflog entry) from
// one that has committed — the signal prune uses to keep the former but reap the
// latter once it's fast-forwarded into base.
func TestBranchAdvanced(t *testing.T) {
	ctx := context.Background()
	r, _ := Init(ctx, t.TempDir())
	commitFile(t, r, "a", "1", "init")

	// Brand-new branch: created, never committed → not advanced.
	if err := r.Checkout(ctx, "fresh", true); err != nil {
		t.Fatal(err)
	}
	if adv, err := r.BranchAdvanced(ctx, "fresh"); err != nil || adv {
		t.Fatalf("fresh branch: advanced=%v err=%v, want false", adv, err)
	}

	// One commit moves the tip → advanced, and stays so even after the work
	// fast-forwards into main (the case that bit prune).
	commitFile(t, r, "b", "2", "work")
	if adv, err := r.BranchAdvanced(ctx, "fresh"); err != nil || !adv {
		t.Fatalf("committed branch: advanced=%v err=%v, want true", adv, err)
	}
	if err := r.Checkout(ctx, "main", false); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, r.Dir, "merge", "--ff-only", "fresh"); err != nil {
		t.Fatal(err)
	}
	if adv, err := r.BranchAdvanced(ctx, "fresh"); err != nil || !adv {
		t.Fatalf("ff-merged branch: advanced=%v err=%v, want true", adv, err)
	}
}

func TestLocalBranches(t *testing.T) {
	ctx := context.Background()
	r, _ := Init(ctx, t.TempDir())
	commitFile(t, r, "a", "1", "init")
	if err := r.Checkout(ctx, "feature", true); err != nil {
		t.Fatal(err)
	}
	if err := r.Checkout(ctx, "main", false); err != nil {
		t.Fatal(err)
	}

	got, err := r.LocalBranches(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Branch{}
	for _, b := range got {
		byName[b.Name] = b
	}
	if len(byName) != 2 {
		t.Fatalf("want 2 branches, got %d: %+v", len(byName), got)
	}
	if !byName["main"].Current {
		t.Errorf("main should be current: %+v", byName["main"])
	}
	if byName["feature"].Current {
		t.Errorf("feature should not be current: %+v", byName["feature"])
	}
	if byName["feature"].Gone {
		t.Errorf("feature has no upstream, should not be gone: %+v", byName["feature"])
	}
}

// A branch whose upstream the remote has deleted reads as Gone — the signal
// branch prune --gone keys on once a merge can no longer be proven locally.
func TestLocalBranchesGoneUpstream(t *testing.T) {
	ctx := context.Background()
	remote := t.TempDir()
	if _, err := runGit(ctx, remote, "init", "--bare", "-b", "main"); err != nil {
		t.Fatal(err)
	}
	r, _ := Init(ctx, t.TempDir())
	commitFile(t, r, "a", "1", "init")
	if _, err := runGit(ctx, r.Dir, "remote", "add", "origin", remote); err != nil {
		t.Fatal(err)
	}
	// Push a feature branch with tracking, then delete it on the remote and prune.
	if err := r.Checkout(ctx, "feature", true); err != nil {
		t.Fatal(err)
	}
	commitFile(t, r, "b", "2", "feature work")
	if _, err := runGit(ctx, r.Dir, "push", "-u", "origin", "feature"); err != nil {
		t.Fatal(err)
	}
	if err := r.Checkout(ctx, "main", false); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, r.Dir, "push", "origin", "--delete", "feature"); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, r.Dir, "fetch", "--prune", "origin"); err != nil {
		t.Fatal(err)
	}

	got, err := r.LocalBranches(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range got {
		if b.Name == "feature" {
			if !b.Gone {
				t.Fatalf("feature upstream deleted on remote, want Gone: %+v", b)
			}
			return
		}
	}
	t.Fatalf("feature branch not found in %+v", got)
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
