//go:build linux

package desktop

import (
	"os"
	"path/filepath"
	"testing"
)

// The VM image is a sparse file provisioned far larger than what it has
// written, and its cost is the allocated blocks, not the declared length — a
// regression to fi.Size() would have doctor and prune report the ~10 GB it
// claims rather than the space it takes.
func TestDirSize_CountsAllocatedBlocksNotLength(t *testing.T) {
	dir := t.TempDir()
	img := filepath.Join(dir, "rootfs.img")
	f, err := os.Create(img)
	if err != nil {
		t.Fatal(err)
	}
	const claimed = 64 << 20
	if err := f.Truncate(claimed); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	n, err := DirSize(dir)
	if err != nil {
		t.Fatal(err)
	}
	if n >= claimed {
		t.Fatalf("DirSize = %d, want less than the %d the sparse file claims", n, claimed)
	}
}
