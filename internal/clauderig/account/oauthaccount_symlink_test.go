package account

import (
	"os"
	"path/filepath"
	"testing"
)

// ~/.claude.json is routinely symlinked into a dotfiles repo. Replacing the link
// with a regular file detaches it silently: both copies keep working, and they
// diverge from that moment on.
func TestAtomicWriteFileWritesThroughASymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "real.json")
	if err := os.WriteFile(target, []byte(`{"a":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link.json")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if err := atomicWriteFile(link, []byte(`{"a":2}`), 0o644); err != nil {
		t.Fatalf("atomicWriteFile: %v", err)
	}

	fi, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the symlink was replaced by a regular file — the real file is now orphaned")
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"a":2}` {
		t.Fatalf("symlink target = %q, want the new content — the write did not land on the real file", got)
	}
}

func TestAtomicWriteFileStillWritesAPlainFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "plain.json")
	if err := atomicWriteFile(p, []byte(`{"a":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"a":1}` {
		t.Fatalf("content = %q", got)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", fi.Mode().Perm())
	}
}
