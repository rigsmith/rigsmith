//go:build unix

package copytree

import (
	"path/filepath"
	"syscall"
	"testing"
)

func TestCopySkipsIrregularFiles(t *testing.T) {
	src := t.TempDir()
	write(t, filepath.Join(src, "real.txt"), "data")
	// A named pipe (fifo) would block os.Open without O_NONBLOCK; copy must skip it.
	fifo := filepath.Join(src, "pipe")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Skipf("mkfifo unsupported: %v", err)
	}

	dst := filepath.Join(t.TempDir(), "copy")
	st, err := Copy(src, dst, false)
	if err != nil {
		t.Fatalf("Copy: %v", err)
	}
	if has(tree(t, dst), "pipe") {
		t.Error("fifo should be skipped, not copied")
	}
	if st.Files != 1 {
		t.Errorf("Files = %d, want 1 (fifo not counted)", st.Files)
	}
}
