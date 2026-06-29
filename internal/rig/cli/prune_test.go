package cli

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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

// pruneContextLine banners the current checkout: its branch and whether it is the
// repo's primary checkout or a linked worktree (the checkout prune always protects).
func TestPruneContextLine(t *testing.T) {
	ctx := context.Background()
	r, err := gitrepo.Init(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	commit(t, r, "a", "1", "init")
	branch, _ := r.CurrentBranch(ctx)

	primary := pruneContextLine(ctx, r, r.Dir)
	if !strings.Contains(primary, "primary checkout") || !strings.Contains(primary, "protected") {
		t.Errorf("primary checkout banner missing its label: %s", primary)
	}
	if branch != "" && !strings.Contains(primary, branch) {
		t.Errorf("banner should name the current branch %q: %s", branch, primary)
	}

	// A linked worktree is labeled "worktree", not "primary".
	wtPath := filepath.Join(t.TempDir(), "wt")
	if err := r.WorktreeAdd(ctx, wtPath, "feature", branch, true); err != nil {
		t.Fatal(err)
	}
	wtRepo, err := gitrepo.Open(ctx, wtPath)
	if err != nil {
		t.Fatal(err)
	}
	wt := pruneContextLine(ctx, wtRepo, wtPath)
	if !strings.Contains(wt, "worktree") || strings.Contains(wt, "primary") {
		t.Errorf("worktree banner should say worktree, not primary: %s", wt)
	}
	if !strings.Contains(wt, "feature") {
		t.Errorf("worktree banner should name the 'feature' branch: %s", wt)
	}

	// Detached HEAD: `git rev-parse --abbrev-ref HEAD` returns the literal "HEAD",
	// which the banner relabels rather than showing as a branch name.
	if out, err := exec.Command("git", "-C", r.Dir, "checkout", "--detach", "HEAD").CombinedOutput(); err != nil {
		t.Fatalf("detach HEAD: %v: %s", err, out)
	}
	if det := pruneContextLine(ctx, r, r.Dir); !strings.Contains(det, "detached HEAD") {
		t.Errorf("detached checkout should be labeled detached, not 'HEAD': %s", det)
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
	// Advance main past feature so feature is a *merged, diverged* ancestor — not
	// merely even with base, which is kept.
	commit(t, r, "b", "2", "advance main past feature")

	var buf bytes.Buffer
	wRemoved, _, freed, _, err := pruneWorktrees(ctx, &buf, r, r.Dir, "main", false, false, false, nil, nil)
	if err != nil || wRemoved != 1 {
		t.Fatalf("worktree phase: removed=%d err=%v", wRemoved, err)
	}
	detached := map[string]bool{}
	for _, b := range freed {
		detached[b] = true
	}
	bRemoved, _, _, err := pruneBranches(ctx, &buf, r, "main", false, false, detached, nil, nil)
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

	var buf bytes.Buffer
	_, _, freed, _, err := pruneWorktrees(ctx, &buf, r, r.Dir, "main", true /*dryRun*/, false, false, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	detached := map[string]bool{}
	for _, b := range freed {
		detached[b] = true
	}

	// Without the handoff the branch reads as worktree-attached and is skipped.
	if n, _, _, _ := pruneBranches(ctx, &buf, r, "main", true, false, nil, nil, nil); n != 0 {
		t.Errorf("without detached: removed=%d, want 0 (still attached)", n)
	}
	// With it, the branch phase previews the delete.
	if n, _, _, _ := pruneBranches(ctx, &buf, r, "main", true, false, detached, nil, nil); n != 1 {
		t.Errorf("with detached: removed=%d, want 1", n)
	}
	// Dry-run must not have actually touched anything.
	if !r.BranchExists(ctx, "feature") {
		t.Error("dry-run deleted the branch")
	}
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

	var buf bytes.Buffer
	removed, _, _, _, err := pruneWorktrees(ctx, &buf, r, r.Dir, "main", true, false, false, nil, nil)
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

// Counterpart to the brand-new-keep guard: a worktree whose branch did real work
// that then fast-forwarded into base — so its tip is now even with base — must be
// reaped, not mistaken for a never-committed checkout. The reflog distinguishes
// them. This is the regression for `rig prune` silently skipping a freshly
// ff-merged branch.
func TestPruneWorktrees_ReapsFastForwardMerged(t *testing.T) {
	ctx := context.Background()
	r, err := gitrepo.Init(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	commit(t, r, "a", "1", "init")
	// feature does work while main stays put...
	if err := r.Checkout(ctx, "feature", true); err != nil {
		t.Fatal(err)
	}
	commit(t, r, "b", "2", "feature work")
	if err := r.Checkout(ctx, "main", false); err != nil {
		t.Fatal(err)
	}
	// ...then main fast-forwards onto it: feature's tip now equals main's.
	ffMerge(t, r, "feature")
	wtPath := filepath.Join(t.TempDir(), "wt")
	if err := r.WorktreeAdd(ctx, wtPath, "feature", "main", false); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	removed, _, _, _, err := pruneWorktrees(ctx, &buf, r, r.Dir, "main", true /*dryRun*/, false, false, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Errorf("removed=%d, want 1 — a fast-forward-merged worktree must be reaped\n%s", removed, buf.String())
	}
	if !strings.Contains(buf.String(), "fast-forward") {
		t.Errorf("expected a 'merged (fast-forward)' reason, got:\n%s", buf.String())
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

	var buf bytes.Buffer
	w, b, _, err := pruneSweep(ctx, &buf, r, r.Dir, "main", true /*dry*/, false, true /*doWT*/, true /*doBR*/, nil, nil)
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
	if w, b, _, _ := pruneSweep(ctx, &wbuf, r, r.Dir, "main", true, false, true, false, nil, nil); w != 1 || b != 0 {
		t.Errorf("--worktrees sweep = %d/%d; want 1 worktree, 0 branches", w, b)
	}
	var bbuf bytes.Buffer
	// Branches-only: the worktree-attached "feature" is skipped (not freed by a
	// worktree phase), so no branch is counted.
	if w, b, _, _ := pruneSweep(ctx, &bbuf, r, r.Dir, "main", true, false, false, true, nil, nil); w != 0 || b != 0 {
		t.Errorf("--branches sweep = %d/%d; want 0 worktrees, 0 branches (feature still attached)", w, b)
	}
}
