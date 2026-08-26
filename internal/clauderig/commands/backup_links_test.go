package commands

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// ~/.claude is full of shared-memory symlinks (a worktree slug pointing memory/
// at its main project). The pre-restore backup must recreate them as links —
// following one either duplicates the whole linked tree into the .bak or fails
// outright with EISDIR when it points at a directory.
func TestCopyTree_PreservesSymlinksInsteadOfFollowingThem(t *testing.T) {
	src := t.TempDir()
	mem := filepath.Join(src, "projects", "-main", "memory")
	if err := os.MkdirAll(mem, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mem, "MEMORY.md"), []byte("facts"), 0o644); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(src, "projects", "-wt")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(mem, filepath.Join(wt, "memory")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	dst := filepath.Join(t.TempDir(), "claude.bak")
	if err := copyTree(src, dst); err != nil {
		t.Fatalf("backup failed on a shared-memory link: %v", err)
	}

	backedUp := filepath.Join(dst, "projects", "-wt", "memory")
	info, err := os.Lstat(backedUp)
	if err != nil {
		t.Fatalf("link missing from backup: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("link was followed into the backup, not recreated as a link")
	}
	if got, _ := os.Readlink(backedUp); got != mem {
		t.Errorf("link text = %q, want %q", got, mem)
	}
	// the real directory still travels under its own path
	if b, _ := os.ReadFile(filepath.Join(dst, "projects", "-main", "memory", "MEMORY.md")); string(b) != "facts" {
		t.Errorf("memory content = %q, want %q", b, "facts")
	}
}

// ~/.claude holds 0600 transcripts, and the identity file beside it is 0600
// too. os.Create would widen them to 0644 in the .bak, so the act of protecting
// the data would be what exposed it.
func TestCopyOne_PreservesSourcePermissions(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "private.jsonl")
	if err := os.WriteFile(src, []byte("secret transcript"), 0o600); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "bak", "private.jsonl")
	if err := copyOne(src, dst); err != nil {
		t.Fatal(err)
	}
	si, err := os.Stat(src)
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	// Assert the PROPERTY — the backup carries the source's mode — rather than a
	// literal 0600. Windows has no Unix permission bits: Go reports 0666 and
	// Chmod only toggles read-only, so a literal would fail there for a reason
	// that has nothing to do with this code.
	if got := fi.Mode().Perm(); got != si.Mode().Perm() {
		t.Errorf("backup mode = %04o, want %04o (source mode)", got, si.Mode().Perm())
	}
	// Where the bits are real, pin the case that matters: ~/.claude is full of
	// 0600 transcripts, and os.Create would have widened them to 0644.
	if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Errorf("backup mode = %04o, want 0600", fi.Mode().Perm())
	}
	if b, _ := os.ReadFile(dst); string(b) != "secret transcript" {
		t.Errorf("content = %q", b)
	}
}

// A DANGLING symlink at the backup path passes an os.Stat check as "not
// present". The copy would then follow it and write through the link — for
// ~/.claude.json that means an identity file, which can carry MCP credentials,
// landing wherever the link points.
func TestBackupPathIsFree_RejectsADanglingSymlink(t *testing.T) {
	dir := t.TempDir()
	elsewhere := filepath.Join(dir, "elsewhere.json")
	bak := filepath.Join(dir, "claude.json.bak")
	if err := os.Symlink(elsewhere, bak); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	// Stat agrees the target is absent — that is exactly the trap.
	if _, err := os.Stat(bak); !os.IsNotExist(err) {
		t.Fatalf("test setup: want a dangling link, Stat gave %v", err)
	}
	if err := backupPathIsFree(bak); err == nil {
		t.Fatal("a dangling symlink must be refused, not treated as free")
	}
	if _, err := os.Stat(elsewhere); err == nil {
		t.Error("nothing should have been written through the link")
	}
}

func TestBackupPathIsFree_AllowsAnAbsentPath(t *testing.T) {
	if err := backupPathIsFree(filepath.Join(t.TempDir(), "nope.bak")); err != nil {
		t.Errorf("an absent backup path is free: %v", err)
	}
}

// backupPathIsFree and the write are two moments. A symlink created in between
// would be followed by an O_CREATE|O_TRUNC open and its target overwritten, so
// the open itself must refuse an existing entry rather than trusting the
// earlier check.
func TestCopyOne_RefusesADestinationThatAppearedAfterTheCheck(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "identity.json")
	if err := os.WriteFile(src, []byte(`{"secret":"value"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(dir, "victim.txt")
	if err := os.WriteFile(victim, []byte("must survive"), 0o644); err != nil {
		t.Fatal(err)
	}

	// The race: something plants a symlink at dst after the path was checked.
	dst := filepath.Join(dir, "identity.json.bak")
	if err := os.Symlink(victim, dst); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if err := copyOne(src, dst); err == nil {
		t.Error("copyOne must refuse a destination that already exists")
	}
	if b, _ := os.ReadFile(victim); string(b) != "must survive" {
		t.Errorf("wrote through the planted symlink: victim = %q", b)
	}
}

// A plain existing file is refused too — never silently overwritten.
func TestCopyOne_RefusesAnExistingRegularFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "a")
	dst := filepath.Join(dir, "b")
	if err := os.WriteFile(src, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := copyOne(src, dst); err == nil {
		t.Error("want a refusal")
	}
	if b, _ := os.ReadFile(dst); string(b) != "old" {
		t.Errorf("destination was overwritten: %q", b)
	}
}

// If ~/.claude is ITSELF a symlink, WalkDir visits only that entry — and
// recreating it would make .bak a second link to the very directory restore is
// about to modify, so the "backup" would track the changes rather than preserve
// what came before. The root is followed; links below it are still reproduced.
func TestCopyTree_FollowsASymlinkedRootButNotLinksBelowIt(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real-claude")
	if err := os.MkdirAll(filepath.Join(real, "projects", "-main", "memory"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, "projects", "-main", "memory", "MEMORY.md"), []byte("facts"), 0o600); err != nil {
		t.Fatal(err)
	}
	// a link BELOW the root, which must stay a link
	if err := os.Symlink(filepath.Join(real, "projects", "-main", "memory"),
		filepath.Join(real, "projects", "linked")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	// the root itself is a link
	root := filepath.Join(dir, "claude")
	if err := os.Symlink(real, root); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	bak := filepath.Join(dir, "claude.bak")
	if err := copyTree(root, bak); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Lstat(bak)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		t.Fatal("the backup is a symlink to the live tree, so it preserves nothing")
	}
	if b, _ := os.ReadFile(filepath.Join(bak, "projects", "-main", "memory", "MEMORY.md")); string(b) != "facts" {
		t.Errorf("content not copied: %q", b)
	}
	// and the link below the root is still a link
	li, err := os.Lstat(filepath.Join(bak, "projects", "linked"))
	if err != nil || li.Mode()&os.ModeSymlink == 0 {
		t.Errorf("a link below the root should stay a link: %v", err)
	}
}
