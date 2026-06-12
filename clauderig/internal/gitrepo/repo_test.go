package gitrepo

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestMain(m *testing.M) {
	if _, err := exec.LookPath("git"); err != nil {
		// no git available — skip the whole package rather than fail
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func write(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestInitCommitEmptyGuard(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	r, err := Init(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	// nothing staged → no commit
	if changed, err := r.Commit(ctx, "empty"); err != nil || changed {
		t.Fatalf("empty commit guard failed: changed=%v err=%v", changed, err)
	}
	// add a file → commits
	write(t, dir, "settings.json", "{}")
	if changed, err := r.Commit(ctx, "add settings"); err != nil || !changed {
		t.Fatalf("expected commit: changed=%v err=%v", changed, err)
	}
	// re-commit with no change → guard again
	if changed, _ := r.Commit(ctx, "noop"); changed {
		t.Fatal("expected no-op second commit")
	}
	if b, err := r.CurrentBranch(ctx); err != nil || b != "main" {
		t.Fatalf("branch = %q err=%v", b, err)
	}
}

func TestBranchCheckout(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	r, _ := Init(ctx, dir)
	write(t, dir, "a", "1")
	r.Commit(ctx, "init")
	if err := r.Checkout(ctx, "history", true); err != nil {
		t.Fatal(err)
	}
	if b, _ := r.CurrentBranch(ctx); b != "history" {
		t.Fatalf("branch = %q", b)
	}
}

// Full round-trip through a bare remote: push from A, clone into B, see the file;
// then A pushes again and B fast-forward-pulls it.
func TestPushClonePull(t *testing.T) {
	ctx := context.Background()

	bare := t.TempDir()
	if _, err := runGit(ctx, bare, "init", "--bare", "-b", "main"); err != nil {
		t.Fatal(err)
	}

	a, _ := Init(ctx, t.TempDir())
	if err := a.SetRemote(ctx, "origin", bare); err != nil {
		t.Fatal(err)
	}
	if !a.HasRemote(ctx, "origin") {
		t.Fatal("remote not set")
	}
	write(t, a.Dir, "settings.json", "{}")
	a.Commit(ctx, "first")
	if err := a.Push(ctx, "origin", "main"); err != nil {
		t.Fatal(err)
	}

	bDir := filepath.Join(t.TempDir(), "clone")
	b, err := Clone(ctx, bare, bDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(b.Dir, "settings.json")); err != nil {
		t.Fatalf("clone missing file: %v", err)
	}

	// A adds more, pushes; B pulls (ff-only) and sees it.
	write(t, a.Dir, "skills/x.md", "hi")
	a.Commit(ctx, "second")
	a.Push(ctx, "origin", "main")
	if err := b.Pull(ctx, "origin", "main"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(b.Dir, "skills", "x.md")); err != nil {
		t.Fatalf("pull missing file: %v", err)
	}
}

func TestGitDirBytes(t *testing.T) {
	ctx := context.Background()
	r, _ := Init(ctx, t.TempDir())
	write(t, r.Dir, "a", "data")
	r.Commit(ctx, "c")
	n, err := r.GitDirBytes(ctx)
	if err != nil || n <= 0 {
		t.Fatalf("git dir bytes = %d err=%v", n, err)
	}
}
