//go:build windows

package desktop

import (
	"io/fs"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// fileStandardInfo mirrors FILE_STANDARD_INFO: AllocationSize is what the file
// occupies on disk, which for a sparse or compressed file is less than its
// logical length.
type fileStandardInfo struct {
	AllocationSize int64
	EndOfFile      int64
	NumberOfLinks  uint32
	DeletePending  byte
	Directory      byte
}

// diskUsage is the space a file takes on disk. The VM image is sparse, so its
// logical length overstates it by gigabytes; the allocated size is what the
// filesystem reports through FileStandardInfo. Anything that cannot be opened
// falls back to the logical length — an over-estimate, never an under-estimate.
func diskUsage(path string, fi fs.FileInfo) int64 {
	f, err := os.Open(path)
	if err != nil {
		return fi.Size()
	}
	defer f.Close()
	var info fileStandardInfo
	if err := windows.GetFileInformationByHandleEx(windows.Handle(f.Fd()), windows.FileStandardInfo,
		(*byte)(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		return fi.Size()
	}
	return info.AllocationSize
}
