//go:build darwin || linux

package files

import (
	"errors"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"testing"
)

func TestSourceFIFOHasNoBlockingOpen(t *testing.T) {
	root, s := sourceFixture(t)
	if err := unix.Mkfifo(filepath.Join(root, "pipe"), 0o600); err != nil {
		t.Fatal(err)
	}
	if data, err := s.Read(t.Context(), "pipe", 100); !errors.Is(err, ErrSource) || data != nil {
		t.Fatalf("read special file: %v", err)
	}
	// Also cover the open used if a file changes into a FIFO after Lstat.
	f, err := openSourceFile(s.root, "pipe")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Mode()&os.ModeNamedPipe == 0 {
		t.Fatalf("not FIFO: %v", err)
	}
}

func TestSourceOpenRefusesEvenInternalSymlink(t *testing.T) {
	root, s := sourceFixture(t)
	if err := os.WriteFile(filepath.Join(root, "target"), []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	f, err := openSourceFile(s.root, "link")
	if err == nil {
		f.Close()
		t.Fatal("opened internal symlink")
	}
}
