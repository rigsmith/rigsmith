package account

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
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
	// Windows has no Unix permission bits — Go's Chmod there only toggles the
	// read-only flag, so a file written 0600 reports 0666. Asserting the mode
	// would only be asserting the platform.
	if runtime.GOOS == "windows" {
		return
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", fi.Mode().Perm())
	}
}

// A dangling symlink — one whose target has not been created yet — is exactly
// the case EvalSymlinks fails on, and treating that failure as "not a symlink"
// would rename over the link and detach it.
func TestAtomicWriteFileWritesThroughADanglingSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "not-yet.json")
	link := filepath.Join(root, "link.json")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := atomicWriteFile(link, []byte(`{"a":1}`), 0o644); err != nil {
		t.Fatalf("atomicWriteFile: %v", err)
	}
	fi, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the dangling symlink was replaced by a regular file")
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("the link's target was never created: %v", err)
	}
	if string(got) != `{"a":1}` {
		t.Fatalf("target content = %q", got)
	}
}

// A relative link target resolves against the directory holding the link, as
// the OS does — not against the process's working directory.
func TestResolveLinkTargetFollowsRelativeChains(t *testing.T) {
	root := t.TempDir()
	final := filepath.Join(root, "final.json")
	if err := os.WriteFile(final, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	mid := filepath.Join(root, "mid.json")
	if err := os.Symlink("final.json", mid); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	first := filepath.Join(root, "first.json")
	if err := os.Symlink("mid.json", first); err != nil {
		t.Fatal(err)
	}
	if got := resolveLinkTarget(first); got != final {
		t.Fatalf("resolveLinkTarget = %q, want %q", got, final)
	}
}

// A symlink cycle must terminate rather than spin.
func TestResolveLinkTargetSurvivesACycle(t *testing.T) {
	root := t.TempDir()
	a, b := filepath.Join(root, "a"), filepath.Join(root, "b")
	if err := os.Symlink(b, a); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(a, b); err != nil {
		t.Fatal(err)
	}
	done := make(chan string, 1)
	go func() { done <- resolveLinkTarget(a) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("resolveLinkTarget did not terminate on a symlink cycle")
	}
}
