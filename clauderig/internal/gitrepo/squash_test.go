package gitrepo

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func commitCount(t *testing.T, r *Repo) int {
	t.Helper()
	out, err := runGit(context.Background(), r.Dir, "rev-list", "--count", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, c := range out {
		if c >= '0' && c <= '9' {
			n = n*10 + int(c-'0')
		}
	}
	return n
}

func TestSquash_CollapsesHistoryKeepsContent(t *testing.T) {
	ctx := context.Background()
	r, _ := Init(ctx, t.TempDir())
	for i, content := range []string{"a", "ab", "abc"} {
		write(t, r.Dir, "file.txt", content)
		write(t, r.Dir, "keep.txt", "stable")
		if changed, err := r.Commit(ctx, "c"); err != nil || !changed {
			t.Fatalf("commit %d: changed=%v err=%v", i, changed, err)
		}
	}
	if got := commitCount(t, r); got != 3 {
		t.Fatalf("pre-squash commits = %d, want 3", got)
	}

	if err := r.Squash(ctx, "clauderig: squashed history"); err != nil {
		t.Fatal(err)
	}

	if got := commitCount(t, r); got != 1 {
		t.Fatalf("post-squash commits = %d, want 1", got)
	}
	// content intact at latest state
	if b, _ := os.ReadFile(filepath.Join(r.Dir, "file.txt")); string(b) != "abc" {
		t.Errorf("file.txt = %q, want abc", b)
	}
	if b, _ := os.ReadFile(filepath.Join(r.Dir, "keep.txt")); string(b) != "stable" {
		t.Errorf("keep.txt lost: %q", b)
	}
	// working tree is clean after squash (same tree)
	if dirty, _ := r.Dirty(ctx); dirty {
		t.Error("working tree should be clean after squash")
	}
}

func TestShouldSquash(t *testing.T) {
	floor := int64(500 << 20)
	// below floor → never
	if ShouldSquash(100<<20, 10<<20, floor, 2.0) {
		t.Error("below floor should not squash")
	}
	// above floor and > 2x worktree → squash
	if !ShouldSquash(700<<20, 300<<20, floor, 2.0) {
		t.Error("700MB git vs 300MB worktree (>2x) above floor should squash")
	}
	// above floor but git < 2x worktree → no
	if ShouldSquash(700<<20, 600<<20, floor, 2.0) {
		t.Error("700MB git vs 600MB worktree (<2x) should not squash")
	}
}
