package gitrepo

import (
	"context"
	"testing"
)

// divergedPair builds a clone whose HEAD and origin/main have both moved. Each
// side edits `file` with the given content, so passing the same filename to both
// produces a conflicting divergence and different filenames a clean one.
func divergedPair(t *testing.T, localFile, remoteFile string) *Repo {
	t.Helper()
	ctx := context.Background()

	origin, err := Init(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	write(t, origin.Dir, "base", "shared\n")
	if _, err := origin.Commit(ctx, "base"); err != nil {
		t.Fatal(err)
	}

	clone, err := Clone(ctx, origin.Dir, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	// origin moves on
	write(t, origin.Dir, remoteFile, "from origin\n")
	if _, err := origin.Commit(ctx, "origin side"); err != nil {
		t.Fatal(err)
	}
	// the clone moves on independently, then learns about origin without merging
	write(t, clone.Dir, localFile, "from clone\n")
	if _, err := clone.Commit(ctx, "clone side"); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, clone.Dir, "fetch", "origin"); err != nil {
		t.Fatal(err)
	}
	return clone
}

// originRef is the ref the clone's default branch tracks. Init/Clone don't pin a
// branch name, so resolve it rather than assuming main vs master.
func originRef(t *testing.T, r *Repo) string {
	t.Helper()
	b, err := r.CurrentBranch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return "origin/" + b
}

func TestDivergenceUntracked(t *testing.T) {
	ctx := context.Background()
	r, err := Init(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	write(t, r.Dir, "a", "x")
	if _, err := r.Commit(ctx, "only commit"); err != nil {
		t.Fatal(err)
	}

	d, err := r.DivergenceFrom(ctx, "origin/main")
	if err != nil {
		t.Fatal(err)
	}
	// Never fetched is a state to render, not an error.
	if d.Tracked || d.Ahead != 0 || d.Behind != 0 || d.Diverged() {
		t.Fatalf("want untracked zero-value, got %+v", d)
	}
}

func TestDivergenceInSync(t *testing.T) {
	ctx := context.Background()
	origin, err := Init(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	write(t, origin.Dir, "base", "shared\n")
	if _, err := origin.Commit(ctx, "base"); err != nil {
		t.Fatal(err)
	}
	clone, err := Clone(ctx, origin.Dir, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	d, err := clone.DivergenceFrom(ctx, originRef(t, clone))
	if err != nil {
		t.Fatal(err)
	}
	if !d.Tracked || d.Ahead != 0 || d.Behind != 0 || d.Conflict || d.Merging {
		t.Fatalf("fresh clone should be level, got %+v", d)
	}
}

func TestDivergenceBehindOnly(t *testing.T) {
	ctx := context.Background()
	origin, err := Init(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	write(t, origin.Dir, "base", "shared\n")
	if _, err := origin.Commit(ctx, "base"); err != nil {
		t.Fatal(err)
	}
	clone, err := Clone(ctx, origin.Dir, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	write(t, origin.Dir, "later", "more\n")
	if _, err := origin.Commit(ctx, "origin moves"); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, clone.Dir, "fetch", "origin"); err != nil {
		t.Fatal(err)
	}

	d, err := clone.DivergenceFrom(ctx, originRef(t, clone))
	if err != nil {
		t.Fatal(err)
	}
	if d.Ahead != 0 || d.Behind != 1 {
		t.Fatalf("want 0 ahead / 1 behind, got %+v", d)
	}
	// Behind-only fast-forwards, so it must never be reported as diverged or
	// conflicting — that is the difference between the amber and red tray.
	if d.Diverged() || d.Conflict {
		t.Fatalf("behind-only must not be diverged/conflicting, got %+v", d)
	}
}

func TestDivergenceAheadOnly(t *testing.T) {
	ctx := context.Background()
	origin, err := Init(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	write(t, origin.Dir, "base", "shared\n")
	if _, err := origin.Commit(ctx, "base"); err != nil {
		t.Fatal(err)
	}
	clone, err := Clone(ctx, origin.Dir, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	write(t, clone.Dir, "mine", "local\n")
	if _, err := clone.Commit(ctx, "clone moves"); err != nil {
		t.Fatal(err)
	}

	d, err := clone.DivergenceFrom(ctx, originRef(t, clone))
	if err != nil {
		t.Fatal(err)
	}
	if d.Ahead != 1 || d.Behind != 0 || d.Diverged() || d.Conflict {
		t.Fatalf("want 1 ahead / 0 behind, clean, got %+v", d)
	}
}

func TestDivergenceCleanMerge(t *testing.T) {
	ctx := context.Background()
	clone := divergedPair(t, "clone-only.txt", "origin-only.txt")

	d, err := clone.DivergenceFrom(ctx, originRef(t, clone))
	if err != nil {
		t.Fatal(err)
	}
	if !d.Diverged() {
		t.Fatalf("both sides moved, want diverged, got %+v", d)
	}
	// Different files — git merges these without help.
	if d.Conflict {
		t.Fatalf("disjoint edits should not conflict, got %+v", d)
	}
}

func TestDivergenceConflictingMerge(t *testing.T) {
	ctx := context.Background()
	clone := divergedPair(t, "same.txt", "same.txt")

	d, err := clone.DivergenceFrom(ctx, originRef(t, clone))
	if err != nil {
		t.Fatal(err)
	}
	if !d.Diverged() || !d.Conflict {
		t.Fatalf("both sides edited the same file, want diverged+conflict, got %+v", d)
	}
}

// The conflict probe must be a pure read: it may write objects, but the index
// and worktree have to come out untouched, or polling would corrupt the repo.
func TestDivergenceProbeLeavesWorktreeClean(t *testing.T) {
	ctx := context.Background()
	clone := divergedPair(t, "same.txt", "same.txt")

	before, err := runGit(ctx, clone.Dir, "status", "--porcelain=v1", "--branch")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := clone.DivergenceFrom(ctx, originRef(t, clone)); err != nil {
		t.Fatal(err)
	}
	after, err := runGit(ctx, clone.Dir, "status", "--porcelain=v1", "--branch")
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("probe changed the worktree:\nbefore %q\nafter  %q", before, after)
	}
	if dirty, err := clone.Dirty(ctx); err != nil || dirty {
		t.Fatalf("probe left the repo dirty: dirty=%v err=%v", dirty, err)
	}
	if clone.mergeInProgress(ctx) {
		t.Fatal("probe left a merge in progress")
	}
}

func TestDivergenceMergingFlag(t *testing.T) {
	ctx := context.Background()
	clone := divergedPair(t, "same.txt", "same.txt")

	conflicted, err := clone.FetchMerge(ctx, "origin", "")
	if err != nil {
		t.Fatal(err)
	}
	if !conflicted {
		t.Fatal("expected the merge to conflict")
	}

	d, err := clone.DivergenceFrom(ctx, originRef(t, clone))
	if err != nil {
		t.Fatal(err)
	}
	if !d.Merging {
		t.Fatalf("repo is mid-merge, want Merging, got %+v", d)
	}
}
