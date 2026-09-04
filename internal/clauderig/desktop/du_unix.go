//go:build !windows

package desktop

import (
	"io/fs"
	"syscall"
)

// diskUsage is the space a file takes on disk. Blocks are always 512 bytes in
// stat, whatever the filesystem's own block size, so a sparse image reports
// only the extents it has actually written.
func diskUsage(_ string, fi fs.FileInfo) int64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return int64(st.Blocks) * 512
	}
	return fi.Size()
}
