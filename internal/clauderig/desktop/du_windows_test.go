//go:build windows

package desktop

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

// The VM image is a sparse file provisioned far larger than what it has
// written; on NTFS a file is sparse only once marked so, and its cost is then
// the allocated size FileStandardInfo reports, not the declared length. A
// regression to fi.Size() would have doctor and prune report the ~10 GB it
// claims rather than the space it takes.
func TestDiskUsage_SparseFileCountsAllocation(t *testing.T) {
	dir := t.TempDir()
	img := filepath.Join(dir, "rootfs.vhdx")
	f, err := os.Create(img)
	if err != nil {
		t.Fatal(err)
	}
	var ret uint32
	if err := windows.DeviceIoControl(windows.Handle(f.Fd()), windows.FSCTL_SET_SPARSE, nil, 0, nil, 0, &ret, nil); err != nil {
		f.Close()
		t.Skipf("filesystem does not support sparse files: %v", err)
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
