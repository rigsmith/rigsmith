package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/core/gitrepo"
)

// renderPruneTable lays rows out as a name | state | why table with a header.
func TestRenderPruneTable(t *testing.T) {
	var buf bytes.Buffer
	renderPruneTable(&buf, []pruneRow{
		{name: "feat/a", kind: prunePlan, state: "will remove", why: "merged"},
		{name: "feat/longer-name", kind: pruneSkip, state: "skip", why: "uncommitted changes"},
	})
	out := buf.String()
	for _, want := range []string{"name", "state", "why", "feat/a", "will remove", "uncommitted changes"} {
		if !strings.Contains(out, want) {
			t.Errorf("table missing %q\n%s", want, out)
		}
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 3 { // header + 2 rows
		t.Fatalf("got %d lines, want 3 (header + 2 rows):\n%s", len(lines), out)
	}
}

// An empty plan renders a "(none)" line rather than a bare header.
func TestRenderPruneTableEmpty(t *testing.T) {
	var buf bytes.Buffer
	renderPruneTable(&buf, nil)
	if !strings.Contains(buf.String(), "none") {
		t.Errorf("empty table should say (none), got %q", buf.String())
	}
}

// A worktree on a merged branch and the branch itself are cleared by the two
// phases run in order: worktrees first, then branches.
func TestPruneWorktreeThenBranch(t *testing.T) {
	ctx := context.Background()
	r, err := gitrepo.Init(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	commit(t, r, "a", "1", "init")
	wtPath := filepath.Join(t.TempDir(), "wt")
	if err := r.WorktreeAdd(ctx, wtPath, "feature", "main", true); err != nil {
		t.Fatal(err)
	}
	// Advance main past feature so feature is a *merged, diverged* ancestor (not
	// merely even with base, which is now kept), and drop the fresh-checkout grace.
	commit(t, r, "b", "2", "advance main past feature")
	noPruneGrace(t)

	var buf bytes.Buffer
	wRemoved, _, freed, err := pruneWorktrees(ctx, &buf, r, r.Dir, "main", false, false, false)
	if err != nil || wRemoved != 1 {
		t.Fatalf("worktree phase: removed=%d err=%v", wRemoved, err)
	}
	detached := map[string]bool{}
	for _, b := range freed {
		detached[b] = true
	}
	bRemoved, _, err := pruneBranches(ctx, &buf, r, "main", false, false, detached)
	if err != nil || bRemoved != 1 {
		t.Fatalf("branch phase: removed=%d err=%v", bRemoved, err)
	}
	if r.BranchExists(ctx, "feature") {
		t.Error("feature branch should be deleted after both phases")
	}
}

// In dry-run nothing is removed, so the branch is still worktree-attached — the
// freed→detached handoff is what lets the branch phase preview deleting it.
func TestPruneDryRunDetachesFreed(t *testing.T) {
	ctx := context.Background()
	r, err := gitrepo.Init(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	commit(t, r, "a", "1", "init")
	wtPath := filepath.Join(t.TempDir(), "wt")
	if err := r.WorktreeAdd(ctx, wtPath, "feature", "main", true); err != nil {
		t.Fatal(err)
	}
	commit(t, r, "b", "2", "advance main past feature")
	noPruneGrace(t)

	var buf bytes.Buffer
	_, _, freed, err := pruneWorktrees(ctx, &buf, r, r.Dir, "main", true /*dryRun*/, false, false)
	if err != nil {
		t.Fatal(err)
	}
	detached := map[string]bool{}
	for _, b := range freed {
		detached[b] = true
	}

	// Without the handoff the branch reads as worktree-attached and is skipped.
	if n, _, _ := pruneBranches(ctx, &buf, r, "main", true, false, nil); n != 0 {
		t.Errorf("without detached: removed=%d, want 0 (still attached)", n)
	}
	// With it, the branch phase previews the delete.
	if n, _, _ := pruneBranches(ctx, &buf, r, "main", true, false, detached); n != 1 {
		t.Errorf("with detached: removed=%d, want 1", n)
	}
	// Dry-run must not have actually touched anything.
	if !r.BranchExists(ctx, "feature") {
		t.Error("dry-run deleted the branch")
	}
}

// noPruneGrace disables the fresh-checkout grace for a test (the worktrees a test
// creates are seconds old, so the default 10-minute grace would skip them all).
func noPruneGrace(t *testing.T) {
	t.Helper()
	old := pruneFreshness
	pruneFreshness = 0
	t.Cleanup(func() { pruneFreshness = old })
}

// Regression: a brand-new worktree whose branch is even with base (created, never
// committed) must NOT be pruned — it's "merged" only because it has no commits.
func TestPruneWorktrees_KeepsBranchEvenWithBase(t *testing.T) {
	ctx := context.Background()
	r, err := gitrepo.Init(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	commit(t, r, "a", "1", "init")
	wtPath := filepath.Join(t.TempDir(), "wt")
	if err := r.WorktreeAdd(ctx, wtPath, "feature", "main", true); err != nil {
		t.Fatal(err)
	}
	noPruneGrace(t) // isolate the even-with-base guard from the freshness grace

	var buf bytes.Buffer
	removed, _, _, err := pruneWorktrees(ctx, &buf, r, r.Dir, "main", true, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 0 {
		t.Errorf("removed=%d, want 0 — an even-with-base worktree must be kept", removed)
	}
	if !strings.Contains(buf.String(), "even with base") {
		t.Errorf("expected an 'even with base' skip reason, got:\n%s", buf.String())
	}
}

// The grace period keeps a freshly-created worktree even when its branch is a
// genuine, merged prune candidate.
func TestPruneWorktrees_GracePeriodKeepsFresh(t *testing.T) {
	ctx := context.Background()
	r, err := gitrepo.Init(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	commit(t, r, "a", "1", "init")
	wtPath := filepath.Join(t.TempDir(), "wt")
	if err := r.WorktreeAdd(ctx, wtPath, "feature", "main", true); err != nil {
		t.Fatal(err)
	}
	commit(t, r, "b", "2", "advance main past feature") // feature now merged + diverged

	// Default grace in force: the worktree is seconds old, so it's spared.
	var buf bytes.Buffer
	if removed, _, _, _ := pruneWorktrees(ctx, &buf, r, r.Dir, "main", true, false, false); removed != 0 {
		t.Errorf("removed=%d, want 0 — a fresh worktree should be within the grace period", removed)
	}
	if !strings.Contains(buf.String(), "created recently") {
		t.Errorf("expected a 'created recently' skip reason, got:\n%s", buf.String())
	}
	// With the grace off, the same merged worktree is prunable.
	noPruneGrace(t)
	var buf2 bytes.Buffer
	if removed, _, _, _ := pruneWorktrees(ctx, &buf2, r, r.Dir, "main", true, false, false); removed != 1 {
		t.Errorf("removed=%d, want 1 once the grace is lifted", removed)
	}
}

func TestIsRecentWorktree(t *testing.T) {
	now := time.Now()
	if isRecentWorktree(time.Time{}, now, 10*time.Minute) {
		t.Error("zero mtime should never count as recent")
	}
	if isRecentWorktree(now.Add(-time.Minute), now, 0) {
		t.Error("a zero grace disables the check")
	}
	if !isRecentWorktree(now.Add(-time.Minute), now, 10*time.Minute) {
		t.Error("1 minute old within a 10-minute grace should be recent")
	}
	if isRecentWorktree(now.Add(-20*time.Minute), now, 10*time.Minute) {
		t.Error("20 minutes old is past a 10-minute grace")
	}
}

// pruneSweep counts both phases together; in dry mode it touches nothing — this
// is what `prune -n` runs.
func TestPruneSweepDryRun(t *testing.T) {
	ctx := context.Background()
	r, err := gitrepo.Init(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	commit(t, r, "a", "1", "init")
	wtPath := filepath.Join(t.TempDir(), "wt")
	if err := r.WorktreeAdd(ctx, wtPath, "feature", "main", true); err != nil {
		t.Fatal(err)
	}
	commit(t, r, "b", "2", "advance main past feature")
	noPruneGrace(t)

	var buf bytes.Buffer
	w, b, err := pruneSweep(ctx, &buf, r, r.Dir, "main", true /*dry*/, false, true /*doWT*/, true /*doBR*/)
	if err != nil {
		t.Fatal(err)
	}
	// The merged worktree and its branch both show in the plan (freed→detached).
	if w != 1 || b != 1 {
		t.Errorf("dry sweep counts = %d worktrees, %d branches; want 1, 1", w, b)
	}
	if !r.BranchExists(ctx, "feature") {
		t.Error("dry sweep must not delete anything")
	}

	// Selectors scope the sweep to one phase.
	var wbuf bytes.Buffer
	if w, b, _ := pruneSweep(ctx, &wbuf, r, r.Dir, "main", true, false, true, false); w != 1 || b != 0 {
		t.Errorf("--worktrees sweep = %d/%d; want 1 worktree, 0 branches", w, b)
	}
	var bbuf bytes.Buffer
	// Branches-only: the worktree-attached "feature" is skipped (not freed by a
	// worktree phase), so no branch is counted.
	if w, b, _ := pruneSweep(ctx, &bbuf, r, r.Dir, "main", true, false, false, true); w != 0 || b != 0 {
		t.Errorf("--branches sweep = %d/%d; want 0 worktrees, 0 branches (feature still attached)", w, b)
	}
}
