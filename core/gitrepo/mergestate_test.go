package gitrepo

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// conflictedOn leaves repo r mid-merge over one path, with the given contents.
func conflictedOn(t *testing.T, rel string, base, ours, theirs []byte) (context.Context, *Repo) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	r, err := Init(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	must(t, os.WriteFile(filepath.Join(dir, rel), base, 0o644))
	if _, err := r.Commit(ctx, "base"); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, dir, "checkout", "-b", "other"); err != nil {
		t.Fatal(err)
	}
	must(t, os.WriteFile(filepath.Join(dir, rel), theirs, 0o644))
	if _, err := r.Commit(ctx, "theirs"); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, dir, "checkout", "main"); err != nil {
		t.Fatal(err)
	}
	must(t, os.WriteFile(filepath.Join(dir, rel), ours, 0o644))
	if _, err := r.Commit(ctx, "ours"); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, dir, "merge", "other"); err == nil {
		t.Fatal("expected a conflict")
	}
	return ctx, r
}

// merge-file writes its result over the "ours" file IN PLACE, so when it refuses
// the content (binary), that file still holds an unmodified ours. Returning it
// would hand one machine's copy back labelled "kept both machines' lines" — a
// resolution the caller reports to the user as a union that never happened.
func TestUnionMerge_ReportsFailureOnBinary(t *testing.T) {
	bin := func(fill byte, n int) []byte {
		b := make([]byte, n)
		for i := range b {
			b[i] = fill
		}
		b[3] = 0 // NUL — git treats it as binary
		return b
	}
	ctx, r := conflictedOn(t, "blob.bin", bin('a', 40), bin('c', 80), bin('b', 60))

	content, ok := r.UnionMerge(ctx, "blob.bin")
	if ok {
		t.Errorf("union of binary content should report failure, got %d bytes starting %q",
			len(content), string(content[:1]))
	}
}

// A text union still succeeds, so the guard above is not just refusing everything.
func TestUnionMerge_KeepsBothTextSides(t *testing.T) {
	ctx, r := conflictedOn(t, "f.txt",
		[]byte("base\n"), []byte("base\nfrom A\n"), []byte("base\nfrom B\n"))

	content, ok := r.UnionMerge(ctx, "f.txt")
	if !ok {
		t.Fatal("text union should succeed")
	}
	for _, want := range []string{"from A", "from B"} {
		if !strings.Contains(string(content), want) {
			t.Errorf("missing %q in:\n%s", want, content)
		}
	}
}

// In a linked worktree .git is a FILE pointing at the real gitdir, so answering
// this by stat-ing .git/MERGE_HEAD silently reports "no merge" while git reports
// one. clauderig's staging repo is an ordinary clone, but this type is shared —
// rig's worktree verbs open it on worktree dirs.
func TestInMerge_SeesAMergeInsideALinkedWorktree(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	r, err := Init(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	must(t, os.WriteFile(filepath.Join(dir, "f.txt"), []byte("base\n"), 0o644))
	if _, err := r.Commit(ctx, "base"); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, dir, "checkout", "-b", "other"); err != nil {
		t.Fatal(err)
	}
	must(t, os.WriteFile(filepath.Join(dir, "f.txt"), []byte("theirs\n"), 0o644))
	if _, err := r.Commit(ctx, "theirs"); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, dir, "checkout", "main"); err != nil {
		t.Fatal(err)
	}
	must(t, os.WriteFile(filepath.Join(dir, "f.txt"), []byte("ours\n"), 0o644))
	if _, err := r.Commit(ctx, "ours"); err != nil {
		t.Fatal(err)
	}

	wt := filepath.Join(t.TempDir(), "wt")
	if _, err := runGit(ctx, dir, "worktree", "add", "-b", "wtb", wt, "main"); err != nil {
		t.Fatal(err)
	}
	if fi, serr := os.Stat(filepath.Join(wt, ".git")); serr != nil || fi.IsDir() {
		t.Fatalf("expected .git to be a file in a linked worktree (err=%v)", serr)
	}
	if _, err := runGit(ctx, wt, "merge", "other"); err == nil {
		t.Fatal("expected a conflict in the worktree")
	}
	wr, err := Open(ctx, wt)
	if err != nil {
		t.Fatal(err)
	}
	if !wr.InMerge(ctx) {
		t.Error("InMerge missed a merge in progress in a linked worktree")
	}
	// And the main checkout, which is NOT mid-merge, must not claim to be.
	if r.InMerge(ctx) {
		t.Error("InMerge reported a merge in the main checkout, which has none")
	}
}
