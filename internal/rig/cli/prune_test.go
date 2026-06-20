package cli

import (
	"bytes"
	"context"
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

// A worktree on a merged branch and the branch itself are cleared by the two
// phases run in order: worktrees first, then branches.
func TestPruneWorktreeThenBranch(t *testing.T) {
	ctx := context.Background()
	r, err := gitrepo.Init(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	commit(t, r, "a", "1", "init")
	// A worktree branched at main's tip is an ancestor of main → merged + clean.
	wtPath := filepath.Join(t.TempDir(), "wt")
	if err := r.WorktreeAdd(ctx, wtPath, "feature", "main", true); err != nil {
		t.Fatal(err)
	}

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
