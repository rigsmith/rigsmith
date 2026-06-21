package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rigsmith/rigsmith/core/gitrepo"
)

// A worktree prune keeps a not-yet-divergent (even-with-base) worktree, marking
// it forceable; naming it in force removes it anyway.
func TestPruneWorktrees_Force(t *testing.T) {
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

	// Without force: kept, and offered as a forceable skip.
	var buf bytes.Buffer
	removed, _, _, rows, err := pruneWorktrees(ctx, &buf, r, r.Dir, "main", true, false, false, nil, nil)
	if err != nil || removed != 0 {
		t.Fatalf("no-force: removed=%d err=%v, want 0", removed, err)
	}
	if got := forceableSkips(rows); len(got) != 1 || got[0] != "feature" {
		t.Fatalf("forceable skips = %v, want [feature]", got)
	}

	// Dry with force: now planned, reason flagged as forced.
	removed, _, _, rows, err = pruneWorktrees(ctx, &buf, r, r.Dir, "main", true, false, false, nil, map[string]bool{"feature": true})
	if err != nil || removed != 1 {
		t.Fatalf("force dry: removed=%d err=%v, want 1", removed, err)
	}
	if countActed(rows) != 1 {
		t.Errorf("countActed = %d, want 1", countActed(rows))
	}
	if !strings.Contains(buf.String(), "forced") {
		t.Errorf("expected a 'forced' reason in the plan:\n%s", buf.String())
	}

	// Acting with force actually removes the worktree.
	if removed, _, _, _, err := pruneWorktrees(ctx, &buf, r, r.Dir, "main", false, false, false, nil, map[string]bool{"feature": true}); err != nil || removed != 1 {
		t.Fatalf("force act: removed=%d err=%v, want 1", removed, err)
	}
	if _, err := os.Stat(wtPath); !os.IsNotExist(err) {
		t.Errorf("worktree dir still present after forced removal: %v", err)
	}
}

// The primary checkout is never prunable — not even when this session runs from
// a linked worktree and the primary sits on a merged, non-base branch, and not
// even when named with --force. (Regression for the hard-rail-by-path guard.)
func TestPruneWorktrees_NeverPrunesPrimary(t *testing.T) {
	ctx := context.Background()
	r, err := gitrepo.Init(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	commit(t, r, "a", "1", "init")
	// Park the primary on a merged, non-base branch.
	if err := r.Checkout(ctx, "old", true); err != nil {
		t.Fatal(err)
	}
	if err := r.Checkout(ctx, "main", false); err != nil {
		t.Fatal(err)
	}
	commit(t, r, "b", "2", "advance main past old") // old is now merged into main
	if err := r.Checkout(ctx, "old", false); err != nil {
		t.Fatal(err)
	}
	// A separate linked worktree is this session (root); base stays main.
	wtPath := filepath.Join(t.TempDir(), "session")
	if err := r.WorktreeAdd(ctx, wtPath, "session", "main", true); err != nil {
		t.Fatal(err)
	}

	// Even named with --force, the primary must be refused, not planned.
	var buf bytes.Buffer
	removed, _, _, rows, err := pruneWorktrees(ctx, &buf, r, wtPath, "main", true, false, false,
		map[string]bool{"old": true}, map[string]bool{"old": true})
	if err != nil {
		t.Fatal(err)
	}
	if removed != 0 {
		t.Fatalf("removed=%d, want 0 — the primary checkout must never be pruned\n%s", removed, buf.String())
	}
	for _, row := range rows {
		if row.name == "old" && row.kind != pruneSkip {
			t.Errorf("primary 'old' should be a skip row, got %+v", row)
		}
	}
	if !strings.Contains(buf.String(), "primary checkout") {
		t.Errorf("expected a 'primary checkout' skip reason, got:\n%s", buf.String())
	}
}

// A branch prune keeps an unmerged branch unless it's named in force.
func TestPruneBranches_Force(t *testing.T) {
	ctx := context.Background()
	r, err := gitrepo.Init(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	commit(t, r, "a", "1", "init")
	if err := r.Checkout(ctx, "work", true); err != nil {
		t.Fatal(err)
	}
	commit(t, r, "b", "2", "unmerged work")
	if err := r.Checkout(ctx, "main", false); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	removed, _, rows, err := pruneBranches(ctx, &buf, r, "main", true, false, nil, nil, nil)
	if err != nil || removed != 0 {
		t.Fatalf("no-force: removed=%d err=%v, want 0", removed, err)
	}
	if got := forceableSkips(rows); len(got) != 1 || got[0] != "work" {
		t.Fatalf("forceable skips = %v, want [work]", got)
	}

	if removed, _, _, err := pruneBranches(ctx, &buf, r, "main", false, false, nil, nil, map[string]bool{"work": true}); err != nil || removed != 1 {
		t.Fatalf("force act: removed=%d err=%v, want 1", removed, err)
	}
	if r.BranchExists(ctx, "work") {
		t.Error("forced branch should be deleted")
	}
}

// only restricts the sweep to the named items; others aren't even considered.
func TestPruneBranches_OnlyRestricts(t *testing.T) {
	ctx := context.Background()
	r, err := gitrepo.Init(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	commit(t, r, "a", "1", "init")
	for _, name := range []string{"br1", "br2"} {
		if err := r.Checkout(ctx, name, true); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Checkout(ctx, "main", false); err != nil {
		t.Fatal(err)
	}
	commit(t, r, "c", "2", "advance main past both branches") // both now merged ancestors

	var buf bytes.Buffer
	removed, _, rows, err := pruneBranches(ctx, &buf, r, "main", true, false, nil, map[string]bool{"br1": true}, nil)
	if err != nil || removed != 1 {
		t.Fatalf("only=br1: removed=%d err=%v, want 1", removed, err)
	}
	for _, row := range rows {
		if row.name == "br2" {
			t.Errorf("br2 should be out of scope with only=br1, got row %+v", row)
		}
	}
}

// --force with no names is refused before any repo work, with a guiding message.
func TestPruneCmd_ForceRequiresNames(t *testing.T) {
	cmd := newPruneCmd()
	cmd.SetArgs([]string{"--force"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "name the worktree") {
		t.Fatalf("err = %v, want the 'name the worktree(s)/branch(es)' guidance", err)
	}
}

// The confirm screen's [f] sub-mode: toggle a kept item, apply it, and it's
// folded into the plan (force set) and no longer offered.
func TestPruneConfirm_ForceSelect(t *testing.T) {
	preview := func(_ pruneScope, force map[string]bool) (string, pruneCounts, []pruneRow) {
		if force["feat"] {
			return "plan", pruneCounts{worktrees: 1}, []pruneRow{{name: "feat", kind: prunePlan, state: "will remove", why: "forced"}}
		}
		return "plan", pruneCounts{}, []pruneRow{{name: "feat", kind: pruneSkip, state: "skip", why: "not merged", forceable: true}}
	}
	m := pruneConfirmModel{scope: scopeBoth, toggles: true, preview: preview, force: map[string]bool{}, text: "plan", forceable: []string{"feat"}}

	m, _ = sendPrune(m, runes("f"))
	if !m.selecting {
		t.Fatal("f should enter force-select mode")
	}
	m, _ = sendPrune(m, runes(" "))
	if !m.sel["feat"] {
		t.Fatal("space should toggle the item under the cursor")
	}
	m, _ = sendPrune(m, key(tea.KeyEnter))
	if m.selecting {
		t.Error("enter should leave force-select mode")
	}
	if !m.force["feat"] {
		t.Error("applied selection should be in the force set")
	}
	if len(m.forceable) != 0 {
		t.Errorf("forced item should no longer be forceable, got %v", m.forceable)
	}

	// A final enter proceeds, carrying the force set.
	m, cmd := sendPrune(m, key(tea.KeyEnter))
	if !isQuit(cmd) || !m.proceed {
		t.Errorf("enter → quit=%v proceed=%v, want both true", isQuit(cmd), m.proceed)
	}
}

// esc in force-select backs out without applying the pending toggles.
func TestPruneConfirm_ForceSelectEscDiscards(t *testing.T) {
	preview := func(_ pruneScope, _ map[string]bool) (string, pruneCounts, []pruneRow) {
		return "plan", pruneCounts{}, []pruneRow{{name: "feat", kind: pruneSkip, state: "skip", why: "not merged", forceable: true}}
	}
	m := pruneConfirmModel{scope: scopeBoth, toggles: true, preview: preview, force: map[string]bool{}, text: "plan", forceable: []string{"feat"}}

	m, _ = sendPrune(m, runes("f"))
	m, _ = sendPrune(m, runes(" "))
	m, _ = sendPrune(m, key(tea.KeyEsc))
	if m.selecting {
		t.Error("esc should leave force-select mode")
	}
	if m.force["feat"] {
		t.Error("esc must not apply the pending selection")
	}
}
