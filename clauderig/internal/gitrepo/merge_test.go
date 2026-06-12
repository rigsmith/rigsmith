package gitrepo

import (
	"context"
	"path/filepath"
	"testing"
)

// twoClones sets up a bare remote with one commit, plus repos A and B both
// cloned/synced from it, ready to diverge.
func twoClones(t *testing.T) (ctx context.Context, a, b *Repo) {
	t.Helper()
	ctx = context.Background()
	bare := filepath.Join(t.TempDir(), "remote.git")
	if _, err := runGit(ctx, filepath.Dir(bare), "init", "--bare", "-b", "main", filepath.Base(bare)); err != nil {
		t.Fatal(err)
	}
	a, _ = Init(ctx, t.TempDir())
	must(t, a.SetRemote(ctx, "origin", bare))
	write(t, a.Dir, "f.txt", "base\n")
	if _, err := a.Commit(ctx, "base"); err != nil {
		t.Fatal(err)
	}
	must(t, a.Push(ctx, "origin", "main"))

	bDir := filepath.Join(t.TempDir(), "b")
	b, _ = Clone(ctx, bare, bDir)
	return ctx, a, b
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestFetchMerge_CleanWhenDifferentFiles(t *testing.T) {
	ctx, a, b := twoClones(t)
	// A advances f.txt and pushes; B touches a DIFFERENT file.
	write(t, a.Dir, "f.txt", "from-A\n")
	a.Commit(ctx, "a change")
	must(t, a.Push(ctx, "origin", "main"))
	write(t, b.Dir, "g.txt", "from-B\n")
	b.Commit(ctx, "b change")

	if err := b.Push(ctx, "origin", "main"); err == nil {
		t.Fatal("expected B's push to be rejected (behind)")
	}
	conflicted, err := b.FetchMerge(ctx, "origin", "main")
	if err != nil || conflicted {
		t.Fatalf("expected clean merge, got conflicted=%v err=%v", conflicted, err)
	}
	if err := b.Push(ctx, "origin", "main"); err != nil {
		t.Fatalf("push after clean merge: %v", err)
	}
}

func TestFetchMerge_ConflictWhenSameFile(t *testing.T) {
	ctx, a, b := twoClones(t)
	// Both edit f.txt differently.
	write(t, a.Dir, "f.txt", "from-A\n")
	a.Commit(ctx, "a change")
	must(t, a.Push(ctx, "origin", "main"))
	write(t, b.Dir, "f.txt", "from-B\n")
	b.Commit(ctx, "b change")

	conflicted, err := b.FetchMerge(ctx, "origin", "main")
	if err != nil {
		t.Fatal(err)
	}
	if !conflicted {
		t.Fatal("expected a conflict on the shared file")
	}
	// caller would run mergetool; here we just back out cleanly
	must(t, b.AbortMerge(ctx))
	if dirty, _ := b.Dirty(ctx); dirty {
		t.Error("abort should restore a clean tree")
	}
}
