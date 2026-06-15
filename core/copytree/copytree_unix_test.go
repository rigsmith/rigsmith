//go:build unix

package copytree

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestCopyPreservesExecutableBit(t *testing.T) {
	src := t.TempDir()
	script := filepath.Join(src, "run.sh")
	write(t, script, "#!/bin/sh\n")
	if err := os.Chmod(script, 0o755); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "copy")
	if _, err := Copy(src, dst, false); err != nil {
		t.Fatalf("Copy: %v", err)
	}
	info, err := os.Stat(filepath.Join(dst, "run.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o100 == 0 {
		t.Errorf("executable bit not preserved: mode %v", info.Mode())
	}
}

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
