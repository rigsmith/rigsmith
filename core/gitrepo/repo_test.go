package gitrepo

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// Reproduces the state that hid ten days of failed backups: two machines sharing
// one staging repo, each committing, neither able to fast-forward. The local
// repo keeps making perfectly good commits — so anything reading "last commit"
// reports health. Only ahead/behind tells the truth.
func TestAheadBehind(t *testing.T) {
	ctx := context.Background()
	bare := t.TempDir()
	if _, err := runGit(ctx, bare, "init", "--bare", "-b", "main"); err != nil {
		t.Fatal(err)
	}

	a, _ := Init(ctx, t.TempDir())
	if err := a.SetRemote(ctx, "origin", bare); err != nil {
		t.Fatal(err)
	}
	write(t, a.Dir, "settings.json", "{}")
	a.Commit(ctx, "first")
	if err := a.Push(ctx, "origin", "main"); err != nil {
		t.Fatal(err)
	}
	if ahead, behind, known, err := a.AheadBehind(ctx, "origin", "main"); err != nil || !known || ahead != 0 || behind != 0 {
		t.Fatalf("just pushed: ahead=%d behind=%d known=%v err=%v, want 0/0 known", ahead, behind, known, err)
	}

	// The other machine pushes twice.
	bDir := filepath.Join(t.TempDir(), "clone")
	b, err := Clone(ctx, bare, bDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"b1", "b2"} {
		write(t, b.Dir, n+".json", "{}")
		b.Commit(ctx, n)
	}
	if err := b.Push(ctx, "origin", "main"); err != nil {
		t.Fatal(err)
	}

	// This machine commits three times and cannot push.
	for _, n := range []string{"a1", "a2", "a3"} {
		write(t, a.Dir, n+".json", "{}")
		a.Commit(ctx, n)
	}
	if err := a.Push(ctx, "origin", "main"); err == nil {
		t.Fatal("push should have been rejected — the remote advanced")
	}
	if _, err := runGit(ctx, a.Dir, "fetch", "origin"); err != nil {
		t.Fatal(err)
	}
	ahead, behind, known, err := a.AheadBehind(ctx, "origin", "main")
	if err != nil {
		t.Fatal(err)
	}
	if !known {
		t.Fatal("tracking ref exists but known=false")
	}
	if ahead != 3 || behind != 2 {
		t.Fatalf("ahead=%d behind=%d, want 3/2", ahead, behind)
	}
}

// "Cannot tell" is its own answer: not an error, not lost work, and — the case
// that matters — not success either. A repo committing against a remote it has
// never reached must not read as up to date with it.
func TestAheadBehind_NoRemoteTrackingRef(t *testing.T) {
	ctx := context.Background()
	a, _ := Init(ctx, t.TempDir())
	write(t, a.Dir, "settings.json", "{}")
	a.Commit(ctx, "first")
	ahead, behind, known, err := a.AheadBehind(ctx, "origin", "main")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if known {
		t.Fatal("known=true with no remote-tracking ref — callers would report being in sync with a remote never reached")
	}
	if ahead != 0 || behind != 0 {
		t.Fatalf("ahead=%d behind=%d, want 0/0 alongside known=false", ahead, behind)
	}
}

func TestReplacePath(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		c := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
		c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	write := func(rel, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, rel)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	git("init", "-q", "-b", "main", dir)
	write("lib/keep.txt", "old")
	write("lib/gone-later.txt", "also old")
	write("other/untouched.txt", "elsewhere")
	git("add", "-A")
	git("commit", "-qm", "the older revision")
	git("tag", "older")

	write("lib/keep.txt", "new")
	os.Remove(filepath.Join(dir, "lib/gone-later.txt"))
	write("lib/added.txt", "only in the newer one")
	git("add", "-A")
	git("commit", "-qm", "the newer revision")

	repo, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.ReplacePath(ctx, "older", "lib"); err != nil {
		t.Fatal(err)
	}

	// Restored, deleted and re-added: a merge with an ancestor does none of this.
	for rel, want := range map[string]string{"lib/keep.txt": "old", "lib/gone-later.txt": "also old"} {
		got, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		if string(got) != want {
			t.Errorf("%s = %q, want %q", rel, got, want)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "lib/added.txt")); !os.IsNotExist(err) {
		t.Error("lib/added.txt survived; it is absent from the target revision")
	}
	if _, err := os.Stat(filepath.Join(dir, "other/untouched.txt")); err != nil {
		t.Error("other/ was touched; only the named path should change")
	}
}

// RemoveTree never runs git rm on the repository itself or its metadata, and
// MergeBase tells "no common ancestor" (exit 1) from "no such revision"
// (exit 128) by the exit status, not by a substring of it.
func TestRemoveTreeGuardsAndMergeBaseErrors(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	r, err := Init(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Commit(ctx, "one"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{".", "..", ".git", ".git/refs", "../elsewhere"} {
		if err := r.RemoveTree(ctx, bad); err == nil {
			t.Errorf("RemoveTree(%q) succeeded", bad)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, ".git", "HEAD")); err != nil {
		t.Fatal("a refused RemoveTree touched .git")
	}
	if out, err := runGit(ctx, dir, "ls-files"); err != nil || strings.TrimSpace(out) != "a.txt" {
		t.Fatalf("a refused RemoveTree touched the index: %q, %v", out, err)
	}
	if _, err := r.MergeBase(ctx, "HEAD", "no-such-revision"); err == nil {
		t.Error("MergeBase with an unknown revision returned no error")
	}
	if base, err := r.MergeBase(ctx, "HEAD", "HEAD"); err != nil || base == "" {
		t.Errorf("MergeBase(HEAD, HEAD) = %q, %v", base, err)
	}
	if err := r.DeleteRef(ctx, "refs/rigsmith/none"); err != nil {
		t.Errorf("deleting an absent ref: %v", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := r.DeleteRef(cancelled, "refs/rigsmith/none"); err == nil {
		t.Error("a cancelled DeleteRef reported success")
	}
}

func TestLogRange(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	r, err := Init(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	write(t, dir, "a", "1")
	if _, err := r.Commit(ctx, "first"); err != nil {
		t.Fatal(err)
	}
	base, err := r.Head(ctx)
	if err != nil {
		t.Fatal(err)
	}
	write(t, dir, "b", "2")
	if _, err := r.Commit(ctx, "second: with a subject that has spaces"); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "c", "3")
	if _, err := r.Commit(ctx, "third"); err != nil {
		t.Fatal(err)
	}
	head, err := r.Head(ctx)
	if err != nil {
		t.Fatal(err)
	}

	got, err := r.LogRange(ctx, base, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	// Newest first, the base itself excluded, the whole subject kept.
	if len(got) != 2 || got[0].SHA != head || got[0].Subject != "third" || got[1].Subject != "second: with a subject that has spaces" {
		t.Fatalf("LogRange = %+v", got)
	}
	if len(got[1].SHA) != 40 {
		t.Fatalf("SHA %q is not a full id", got[1].SHA)
	}

	// A subject is any bytes but a newline, control characters included: the
	// framing must not hand part of one back as the separator.
	write(t, dir, "d", "4")
	odd := "unit\x1fseparator\x1f inside"
	if _, err := r.Commit(ctx, odd); err != nil {
		t.Fatal(err)
	}
	if got, err := r.LogRange(ctx, head, "HEAD"); err != nil || len(got) != 1 || got[0].Subject != odd {
		t.Fatalf("LogRange with an odd subject = %+v, %v; want subject %q intact", got, err, odd)
	}

	// Nothing between a commit and itself: an empty list, not an error.
	if same, err := r.LogRange(ctx, "HEAD", "HEAD"); err != nil || len(same) != 0 {
		t.Fatalf("LogRange(HEAD, HEAD) = %+v, %v", same, err)
	}
	// An unknown base is an error the caller sees, not an empty answer.
	if _, err := r.LogRange(ctx, strings.Repeat("0", 40), "HEAD"); err == nil {
		t.Fatal("LogRange accepted a base git does not have")
	}
}

// DirtyExcluding lets a caller keep bookkeeping subtrees out of the "is there
// unfinished work" question, without going blind to real changes beside them.
func TestDirtyExcluding(t *testing.T) {
	ctx := context.Background()
	r, err := Init(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	write(t, r.Dir, "tracked.txt", "base\n")
	if _, err := r.Commit(ctx, "base"); err != nil {
		t.Fatal(err)
	}

	if dirty, err := r.Dirty(ctx); err != nil || dirty {
		t.Fatalf("clean tree reported dirty=%v err=%v", dirty, err)
	}

	// An untracked file in the excluded subtree doesn't count...
	write(t, r.Dir, "bookkeeping/log.jsonl", "{}\n")
	dirty, err := r.DirtyExcluding(ctx, "bookkeeping")
	if err != nil {
		t.Fatal(err)
	}
	if dirty {
		t.Error("excluded subtree still counted as dirty")
	}
	// ...but plain Dirty still sees it, so the exclusion is opt-in.
	if dirty, err := r.Dirty(ctx); err != nil || !dirty {
		t.Errorf("Dirty should still see the untracked file: dirty=%v err=%v", dirty, err)
	}

	// A real change alongside it must still register.
	write(t, r.Dir, "tracked.txt", "changed\n")
	dirty, err = r.DirtyExcluding(ctx, "bookkeeping")
	if err != nil {
		t.Fatal(err)
	}
	if !dirty {
		t.Error("a real change next to the excluded subtree was missed")
	}
}

// CommitPaths records one file and leaves everything else exactly where it was.
// A tool committing a fact of its own must not sweep a user's half-finished
// edits in with it — which is what Commit, staging everything, would do.
func TestCommitPathsLeavesOtherWorkAlone(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	r, err := Init(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	write(t, dir, "tracked.txt", "first\n")
	write(t, dir, "manifest.json", "{}\n")
	if _, err := r.Commit(ctx, "initial"); err != nil {
		t.Fatal(err)
	}

	// Two dirty files; only one is ours to record.
	write(t, dir, "manifest.json", `{"lastPropose":"branch"}`+"\n")
	write(t, dir, "tracked.txt", "someone's work in progress\n")
	write(t, dir, "untracked.txt", "also theirs\n")

	changed, err := r.CommitPaths(ctx, "record: the manifest", filepath.Join(dir, "manifest.json"))
	if err != nil || !changed {
		t.Fatalf("CommitPaths: changed=%v err=%v", changed, err)
	}
	dirty, err := r.DirtyPaths(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(dirty, " "), "tracked.txt") {
		t.Errorf("the user's edit was swept into the commit; dirty = %v", dirty)
	}
	if !strings.Contains(strings.Join(dirty, " "), "untracked.txt") {
		t.Errorf("an untracked file was swept in; dirty = %v", dirty)
	}
	if body, _ := runGit(ctx, dir, "show", "HEAD:manifest.json"); !strings.Contains(body, "lastPropose") {
		t.Errorf("the named path was not recorded: %q", body)
	}
	if body, _ := runGit(ctx, dir, "show", "HEAD:tracked.txt"); strings.Contains(body, "work in progress") {
		t.Error("the user's edit reached the commit")
	}
}

// Nothing to record is a no-op, not a failure: git says so on stdout and exits
// non-zero, so a caller reading only the error sees an empty message and cannot
// tell the two apart.
func TestCommitPathsNoOpWhenNothingChanged(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	r, err := Init(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	write(t, dir, "manifest.json", "{}\n")
	if _, err := r.Commit(ctx, "initial"); err != nil {
		t.Fatal(err)
	}
	before, _ := r.Head(ctx)

	changed, err := r.CommitPaths(ctx, "record: nothing", filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatalf("a no-op reported an error: %v", err)
	}
	if changed {
		t.Error("reported a commit it did not make")
	}
	if after, _ := r.Head(ctx); after != before {
		t.Error("an empty commit was made")
	}
}

// A path git does not know yet still has to be recordable. `git commit -- <p>`
// refuses an untracked path, and the status check above reports one as a
// change — so without staging, CommitPaths would see work to do and then fail
// to do it.
func TestCommitPathsRecordsAnUntrackedPath(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	r, err := Init(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	write(t, dir, "seed.txt", "seed\n")
	if _, err := r.Commit(ctx, "initial"); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "brand-new.json", `{"recorded":true}`+"\n")
	write(t, dir, "theirs.txt", "not ours to commit\n")

	changed, err := r.CommitPaths(ctx, "record: a new file", filepath.Join(dir, "brand-new.json"))
	if err != nil || !changed {
		t.Fatalf("CommitPaths on an untracked path: changed=%v err=%v", changed, err)
	}
	if body, gerr := runGit(ctx, dir, "show", "HEAD:brand-new.json"); gerr != nil || !strings.Contains(body, "recorded") {
		t.Errorf("the new path was not recorded: %q %v", body, gerr)
	}
	dirty, _ := r.DirtyPaths(ctx)
	if !strings.Contains(strings.Join(dirty, " "), "theirs.txt") {
		t.Errorf("staging the named path swept up another; dirty = %v", dirty)
	}
}

// SetRemote only adds. A caller that meant "origin IS this url" had to check
// first and, in practice, skipped the repoint — so a remote changed in the
// config kept pushing to the old one.
func TestEnsureRemoteAddsThenRepoints(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	repo, err := Init(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.EnsureRemote(ctx, "origin", "https://example.com/first.git"); err != nil {
		t.Fatal(err)
	}
	if err := repo.EnsureRemote(ctx, "origin", "https://example.com/second.git"); err != nil {
		t.Fatal(err)
	}
	out, err := runGit(ctx, dir, "remote", "get-url", "origin")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out); got != "https://example.com/second.git" {
		t.Errorf("origin = %q, want the repointed url", got)
	}
}
