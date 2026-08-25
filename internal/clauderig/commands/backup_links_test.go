package commands

import (
	"os"
	"path/filepath"
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
