package gitrepo

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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

// A merge that stops on conflicts can be settled path by path in favour of
// this side, whichever shape the conflict takes, and then committed.
func TestResolveOurs(t *testing.T) {
	ctx, a, b := twoClones(t)
	// Both sides start from the same base holding both files.
	for _, f := range []string{"keep.txt", "gone.txt"} {
		must(t, os.WriteFile(filepath.Join(a.Dir, f), []byte("base\n"), 0o644))
	}
	if _, err := a.Commit(ctx, "base"); err != nil {
		t.Fatal(err)
	}
	must(t, a.Push(ctx, "origin", "main"))
	must(t, b.Pull(ctx, "origin", "main"))

	// Ours modifies both; theirs modifies keep.txt differently and deletes
	// gone.txt — a UU and a UD.
	must(t, os.WriteFile(filepath.Join(a.Dir, "keep.txt"), []byte("ours\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(a.Dir, "gone.txt"), []byte("ours\n"), 0o644))
	if _, err := a.Commit(ctx, "ours"); err != nil {
		t.Fatal(err)
	}
	must(t, os.WriteFile(filepath.Join(b.Dir, "keep.txt"), []byte("theirs\n"), 0o644))
	must(t, os.Remove(filepath.Join(b.Dir, "gone.txt")))
	if _, err := b.Commit(ctx, "theirs"); err != nil {
		t.Fatal(err)
	}
	must(t, b.Push(ctx, "origin", "main"))

	conflicted, err := a.FetchMerge(ctx, "origin", "main")
	if err != nil || !conflicted {
		t.Fatalf("conflicted=%v err=%v, want a conflict", conflicted, err)
	}
	paths, err := a.UnmergedPaths(ctx)
	if err != nil || len(paths) != 2 {
		t.Fatalf("unmerged = %v, %v", paths, err)
	}
	must(t, a.ResolveOurs(ctx, paths))
	if left, _ := a.UnmergedPaths(ctx); len(left) != 0 {
		t.Fatalf("still unmerged: %v", left)
	}
	must(t, a.CommitMerge(ctx))
	for f, want := range map[string]string{"keep.txt": "ours", "gone.txt": "ours"} {
		got, err := os.ReadFile(filepath.Join(a.Dir, f))
		// Trimmed: a Windows checkout with autocrlf hands the file back CRLF.
		if err != nil || strings.TrimSpace(string(got)) != want {
			t.Errorf("%s = %q, %v; want %q", f, got, err, want)
		}
	}
	if dirty, _ := a.Dirty(ctx); dirty {
		t.Error("merge not committed cleanly")
	}
}
