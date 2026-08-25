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
