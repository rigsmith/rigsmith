package desktop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// shareFixture builds a store with one profile plus a shared root.
func shareFixture(t *testing.T) (*Store, Profile, string) {
	t.Helper()
	s := newTestStore(t)
	p, err := s.Create("work", "", "")
	if err != nil {
		t.Fatal(err)
	}
	return s, p, filepath.Join(t.TempDir(), "shared-root")
}

func TestShareMigratesExistingHistoryAndLinks(t *testing.T) {
	_, p, root := shareFixture(t)
	// The profile has its own history, partitioned by account uuid as Desktop
	// writes it.
	own := filepath.Join(p.DataDir(), "claude-code-sessions", "acct-uuid-a")
	writeFile(t, filepath.Join(own, "session-1.json"), `{"id":1}`)

	results, err := Share(p, root, SharedDirs)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Migrated != 1 {
		t.Fatalf("results = %+v, want one directory with one migrated file", results)
	}
	// The file is in the shared tree...
	shared := filepath.Join(root, "claude-code-sessions", "acct-uuid-a", "session-1.json")
	if got := readFile(t, shared); got != `{"id":1}` {
		t.Fatalf("shared copy = %q", got)
	}
	// ...and still reachable through the profile, which is now a link.
	link := filepath.Join(p.DataDir(), "claude-code-sessions")
	fi, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the profile's session directory is not a link")
	}
	if got := readFile(t, filepath.Join(link, "acct-uuid-a", "session-1.json")); got != `{"id":1}` {
		t.Fatalf("through the link = %q", got)
	}
}

// Two profiles signed into DIFFERENT accounts land in different uuid
// subdirectories — the property that makes sharing safe at all.
func TestShareMergesTwoProfilesWithoutCollision(t *testing.T) {
	s, work, root := shareFixture(t)
	personal, err := s.Create("personal", "", "")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(work.DataDir(), "claude-code-sessions", "acct-a", "s1.json"), "a1")
	writeFile(t, filepath.Join(personal.DataDir(), "claude-code-sessions", "acct-b", "s2.json"), "b2")

	for _, p := range []Profile{work, personal} {
		if _, serr := Share(p, root, SharedDirs); serr != nil {
			t.Fatal(serr)
		}
	}
	if got := readFile(t, filepath.Join(root, "claude-code-sessions", "acct-a", "s1.json")); got != "a1" {
		t.Fatalf("work history = %q", got)
	}
	if got := readFile(t, filepath.Join(root, "claude-code-sessions", "acct-b", "s2.json")); got != "b2" {
		t.Fatalf("personal history = %q", got)
	}
	// Each profile now sees BOTH, which is the point of sharing.
	for _, p := range []Profile{work, personal} {
		for _, rel := range []string{"acct-a/s1.json", "acct-b/s2.json"} {
			if _, err := os.Stat(filepath.Join(p.DataDir(), "claude-code-sessions", rel)); err != nil {
				t.Errorf("%s cannot see %s: %v", p.Name, rel, err)
			}
		}
	}
}

// Migration must never overwrite: the shared tree may hold the default profile's
// own history, and clobbering it would destroy sessions this feature exists to
// preserve. But it must not DISCARD the profile's version either — that was the
// hole this test used to demonstrate without noticing.
func TestShareKeepsTheSharedCopyAndPreservesTheDifferingOne(t *testing.T) {
	_, p, root := shareFixture(t)
	shared := filepath.Join(root, "claude-code-sessions", "acct-a", "s1.json")
	writeFile(t, shared, "ORIGINAL")
	writeFile(t, filepath.Join(p.DataDir(), "claude-code-sessions", "acct-a", "s1.json"), "INCOMING")

	results, err := Share(p, root, SharedDirs)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Conflicts != 1 || results[0].Migrated != 0 || results[0].Skipped != 0 {
		t.Fatalf("results = %+v, want the collision recorded as a conflict", results)
	}
	// The shared tree is untouched...
	if got := readFile(t, shared); got != "ORIGINAL" {
		t.Fatalf("shared file = %q, want ORIGINAL — migration overwrote existing history", got)
	}
	// ...and the profile's differing version still exists somewhere.
	preserved := filepath.Join(results[0].ConflictDir, "acct-a", "s1.json")
	if got := readFile(t, preserved); got != "INCOMING" {
		t.Fatalf("preserved file = %q, want INCOMING — the profile's only copy was destroyed", got)
	}
	// Preserved files live outside data/, so Claude Desktop never reads them.
	if strings.HasPrefix(results[0].ConflictDir, p.DataDir()) {
		t.Fatalf("conflicts are inside the profile's data dir (%s) — the app would see them", results[0].ConflictDir)
	}
}

// An identical collision is genuinely redundant and is simply dropped: the same
// session recorded in both trees does not need preserving twice.
func TestShareDropsAnIdenticalCollision(t *testing.T) {
	_, p, root := shareFixture(t)
	writeFile(t, filepath.Join(root, "claude-code-sessions", "acct-a", "s1.json"), "SAME")
	writeFile(t, filepath.Join(p.DataDir(), "claude-code-sessions", "acct-a", "s1.json"), "SAME")

	results, err := Share(p, root, SharedDirs)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Skipped != 1 || results[0].Conflicts != 0 {
		t.Fatalf("results = %+v, want an identical collision skipped, not treated as a conflict", results)
	}
	if results[0].ConflictDir != "" {
		t.Fatalf("ConflictDir = %q, want empty when nothing conflicted", results[0].ConflictDir)
	}
}

func TestShareIsIdempotent(t *testing.T) {
	_, p, root := shareFixture(t)
	writeFile(t, filepath.Join(p.DataDir(), "claude-code-sessions", "acct-a", "s1.json"), "x")
	if _, err := Share(p, root, SharedDirs); err != nil {
		t.Fatal(err)
	}
	results, err := Share(p, root, SharedDirs)
	if err != nil {
		t.Fatalf("second share failed: %v", err)
	}
	if results[0].Migrated != 0 || results[0].Skipped != 0 {
		t.Fatalf("results = %+v, want a no-op on an already-shared profile", results)
	}
	if !ShareStatus(p, root, SharedDirs).Shared(SharedDirs) {
		t.Fatal("profile does not report as shared after sharing twice")
	}
}

func TestShareStatusDistinguishesLinkedFromOwn(t *testing.T) {
	_, p, root := shareFixture(t)
	writeFile(t, filepath.Join(p.DataDir(), "claude-code-sessions", "a", "s.json"), "x")
	st := ShareStatus(p, root, SharedDirs)
	if st.Shared(SharedDirs) {
		t.Fatal("an unshared profile reports as shared")
	}
	if len(st.Own) != 1 {
		t.Fatalf("Own = %v, want the profile's own directory", st.Own)
	}
	if _, err := Share(p, root, SharedDirs); err != nil {
		t.Fatal(err)
	}
	st = ShareStatus(p, root, SharedDirs)
	if !st.Shared(SharedDirs) || len(st.Own) != 0 {
		t.Fatalf("after sharing: linked=%v own=%v", st.Linked, st.Own)
	}
}

// Unsharing must not destroy history — it stops the sharing, nothing more.
func TestUnshareLeavesTheSharedHistoryIntact(t *testing.T) {
	_, p, root := shareFixture(t)
	writeFile(t, filepath.Join(p.DataDir(), "claude-code-sessions", "acct-a", "s1.json"), "keep me")
	if _, err := Share(p, root, SharedDirs); err != nil {
		t.Fatal(err)
	}
	if err := Unshare(p, root, SharedDirs); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(root, "claude-code-sessions", "acct-a", "s1.json")); got != "keep me" {
		t.Fatalf("shared history = %q — unshare destroyed it", got)
	}
	link := filepath.Join(p.DataDir(), "claude-code-sessions")
	fi, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		t.Fatal("the profile is still linked after unshare")
	}
	if !fi.IsDir() {
		t.Fatal("unshare did not leave a usable directory behind")
	}
}

// A link pointing somewhere else (an older shared root) is replaced, not merged
// from — there is nothing of the profile's own in it.
func TestShareReplacesALinkToAnotherTarget(t *testing.T) {
	_, p, root := shareFixture(t)
	elsewhere := filepath.Join(t.TempDir(), "old-shared")
	if err := os.MkdirAll(elsewhere, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(p.DataDir(), "claude-code-sessions")
	if err := os.RemoveAll(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := Share(p, root, SharedDirs); err != nil {
		t.Fatal(err)
	}
	if !ShareStatus(p, root, SharedDirs).Shared(SharedDirs) {
		t.Fatal("the stale link was not repointed at the shared root")
	}
}

func TestShareCreatesTheSharedTreeWhenAbsent(t *testing.T) {
	_, p, root := shareFixture(t)
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("fixture should start without a shared root")
	}
	if _, err := Share(p, root, SharedDirs); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(root, "claude-code-sessions"))
	if err != nil || !fi.IsDir() {
		t.Fatalf("shared tree was not created: %v", err)
	}
}

// The failure that matters: if the link cannot be created, the profile must be
// left exactly as it was. Deleting first would leave it with NO session
// directory, and Claude Desktop would quietly build a fresh empty tree there.
func TestShareRestoresTheOriginalWhenLinkingFails(t *testing.T) {
	_, p, root := shareFixture(t)
	own := filepath.Join(p.DataDir(), "claude-code-sessions")
	writeFile(t, filepath.Join(own, "acct-a", "s1.json"), "history")

	// Force linkDir to fail by occupying the link path with a file that Rename
	// cannot replace... instead, make the parent read-only after migration.
	orig := linkDirFn
	linkDirFn = func(target, link string) error { return errTestLinkFailed }
	t.Cleanup(func() { linkDirFn = orig })

	if _, err := Share(p, root, SharedDirs); err == nil {
		t.Fatal("Share reported success even though linking failed")
	}
	// The profile still has its own directory, with its history in it.
	fi, err := os.Lstat(own)
	if err != nil {
		t.Fatalf("the profile was left with no session directory: %v", err)
	}
	if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("expected the original directory back, got mode %v", fi.Mode())
	}
	if got := readFile(t, filepath.Join(own, "acct-a", "s1.json")); got != "history" {
		t.Fatalf("restored history = %q", got)
	}
	// No stash left lying around.
	if _, serr := os.Stat(own + ".clauderig-stash"); !os.IsNotExist(serr) {
		t.Fatal("a stash directory was left behind")
	}
	// Retrying after the cause is fixed is a no-op on the shared tree, because
	// mergeTree never overwrites what the first attempt already copied.
	linkDirFn = orig
	results, rerr := Share(p, root, SharedDirs)
	if rerr != nil {
		t.Fatalf("retry failed: %v", rerr)
	}
	if results[0].Migrated != 0 || results[0].Skipped != 1 {
		t.Fatalf("retry results = %+v, want the earlier copy recognised, not duplicated", results)
	}
}

func TestUnshareLeavesADirectoryEvenIfItMustRollBack(t *testing.T) {
	_, p, root := shareFixture(t)
	if _, err := Share(p, root, SharedDirs); err != nil {
		t.Fatal(err)
	}
	if err := Unshare(p, root, SharedDirs); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(p.DataDir(), "claude-code-sessions")
	fi, err := os.Lstat(link)
	if err != nil || !fi.IsDir() {
		t.Fatalf("no usable directory after unshare: %v", err)
	}
	if _, serr := os.Stat(link + ".clauderig-new"); !os.IsNotExist(serr) {
		t.Fatal("a scratch directory was left behind")
	}
}

// Share deletes the source once migration reports success, so anything merely
// "skipped" is destroyed rather than moved. Symlinks are therefore recreated.
func TestShareRecreatesSymlinksRatherThanDestroyingThem(t *testing.T) {
	_, p, root := shareFixture(t)
	own := filepath.Join(p.DataDir(), "claude-code-sessions", "acct-a")
	writeFile(t, filepath.Join(own, "real.json"), "payload")
	if err := os.Symlink("real.json", filepath.Join(own, "alias.json")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if _, err := Share(p, root, SharedDirs); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "claude-code-sessions", "acct-a", "alias.json")
	fi, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("the symlink was destroyed by the migration: %v", err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the entry survived but is no longer a symlink")
	}
	if got, _ := os.Readlink(link); got != "real.json" {
		t.Fatalf("link target = %q, want real.json", got)
	}
}

// A link whose target has been deleted is still our link: unshare must replace
// it, not skip it and claim success.
func TestUnshareReplacesADanglingSharedLink(t *testing.T) {
	_, p, root := shareFixture(t)
	if _, err := Share(p, root, SharedDirs); err != nil {
		t.Fatal(err)
	}
	// The shared tree goes away underneath the profile.
	if err := os.RemoveAll(filepath.Join(root, "claude-code-sessions")); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(p.DataDir(), "claude-code-sessions")
	if !linkPointsAt(link, filepath.Join(root, "claude-code-sessions")) {
		t.Fatal("a dangling link is no longer recognised as ours")
	}
	if err := Unshare(p, root, SharedDirs); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("no session directory after unshare: %v", err)
	}
	if fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
		t.Fatal("unshare left the dangling link in place")
	}
}

// A lost O_EXCL race means someone else's copy stands — counting ours as
// migrated would misreport what happened.
func TestCopyFileReportsWhetherItCreatedTheFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	writeFile(t, src, "x")
	dst := filepath.Join(dir, "dst")

	created, err := copyFile(src, dst, 0o600)
	if err != nil || !created {
		t.Fatalf("first copy: created=%v err=%v", created, err)
	}
	created, err = copyFile(src, dst, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("a copy that hit an existing file reported itself as created")
	}
}

// "Something was already there" is not "our copy is safe". Share deletes the
// source once migration reports success, so a collision the winner wrote
// DIFFERENTLY must be preserved, not counted as skipped.
func TestShareTreatsADifferingRaceWinnerAsAConflict(t *testing.T) {
	_, p, root := shareFixture(t)
	// The shared tree already holds a different file at the same path — the
	// same state a lost O_EXCL race leaves behind.
	writeFile(t, filepath.Join(root, "claude-code-sessions", "acct-a", "s1.json"), "THEIRS")
	writeFile(t, filepath.Join(p.DataDir(), "claude-code-sessions", "acct-a", "s1.json"), "OURS")

	results, err := Share(p, root, SharedDirs)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Conflicts != 1 || results[0].Skipped != 0 {
		t.Fatalf("results = %+v, want the differing collision preserved as a conflict", results)
	}
	if got := readFile(t, filepath.Join(results[0].ConflictDir, "acct-a", "s1.json")); got != "OURS" {
		t.Fatalf("preserved copy = %q, want OURS", got)
	}
}

// A conflict path that is already occupied by something DIFFERENT must not be
// treated as "already preserved" — that would delete the source while claiming
// to have saved it.
func TestPreserveConflictNeverOverwritesAnEarlierBackup(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.json")
	writeFile(t, src, "SECOND")
	want := filepath.Join(dir, "conflicts", "src.json")
	writeFile(t, want, "FIRST")

	info, err := os.Lstat(src)
	if err != nil {
		t.Fatal(err)
	}
	if perr := preserveConflict(src, want, info); perr != nil {
		t.Fatal(perr)
	}
	if got := readFile(t, want); got != "FIRST" {
		t.Fatalf("the earlier backup was overwritten: %q", got)
	}
	if got := readFile(t, want+".1"); got != "SECOND" {
		t.Fatalf("the new copy was not preserved alongside it: %q", got)
	}
}

// An identical backup from an earlier run is as good as ours — no numbered
// duplicate should pile up on a retry.
func TestPreserveConflictReusesAnIdenticalBackup(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.json")
	writeFile(t, src, "SAME")
	want := filepath.Join(dir, "conflicts", "src.json")
	writeFile(t, want, "SAME")

	info, _ := os.Lstat(src)
	if err := preserveConflict(src, want, info); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(want + ".1"); !os.IsNotExist(err) {
		t.Fatal("an identical backup was duplicated")
	}
}

// A source symlink colliding with a regular file is a conflict to preserve, not
// a Readlink error that aborts the whole migration.
func TestEntriesEquivalentHandlesMismatchedKinds(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "plain")
	writeFile(t, file, "x")
	link := filepath.Join(dir, "link")
	if err := os.Symlink("plain", link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	same, err := entriesEquivalent(link, file)
	if err != nil {
		t.Fatalf("a symlink/file collision errored instead of reporting a difference: %v", err)
	}
	if same {
		t.Fatal("a symlink and a regular file were reported as equivalent")
	}
}
