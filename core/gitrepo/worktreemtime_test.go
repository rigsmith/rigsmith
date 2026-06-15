package gitrepo

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// worktreeModTime must resolve a worktree's git admin dir whether its `.git`
// pointer is absolute (git's default) or relative (git 2.48+ relative-paths
// mode), and must degrade to a zero time rather than crash when there's nothing
// to stat.
func TestWorktreeModTime(t *testing.T) {
	ctx := context.Background()
	r, err := Init(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	commitFile(t, r, "a", "1", "init")

	wtPath := filepath.Join(filepath.Dir(r.Dir), "wt-feature")
	if err := r.WorktreeAdd(ctx, wtPath, "feature", "main", true); err != nil {
		t.Fatal(err)
	}

	// WorktreeList populates ModTime for every entry from the absolute pointer.
	wts, err := r.WorktreeList(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, wt := range wts {
		if wt.ModTime.IsZero() {
			t.Fatalf("worktree %s has zero ModTime", wt.Path)
		}
	}

	abs := worktreeModTime(wtPath)
	if abs.IsZero() {
		t.Fatal("absolute gitdir pointer resolved to zero time")
	}

	// Rewrite the `.git` file to a relative gitdir and confirm the same time
	// resolves — the regression the relative-path handling guards against.
	dotgit := filepath.Join(wtPath, ".git")
	data, err := os.ReadFile(dotgit)
	if err != nil {
		t.Fatal(err)
	}
	absDir := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(data)), "gitdir:"))
	rel, err := filepath.Rel(wtPath, absDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dotgit, []byte("gitdir: "+rel+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	switch got := worktreeModTime(wtPath); {
	case got.IsZero():
		t.Fatal("relative gitdir pointer resolved to zero time")
	case !got.Equal(abs):
		t.Fatalf("relative gitdir time %v != absolute %v", got, abs)
	}

	// No `.git` at all → zero time (sorted last, never panics).
	if got := worktreeModTime(t.TempDir()); !got.IsZero() {
		t.Fatalf("missing .git: got %v; want zero", got)
	}
}
