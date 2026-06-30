//go:build darwin

package dsstore

import "syscall"

// statInfo returns the file's CNID (inode) and creation time (seconds since the
// Unix epoch) for a file on a mounted HFS+/APFS volume.
func statInfo(path string) (ino uint32, birth int64, err error) {
	var st syscall.Stat_t
	if err = syscall.Stat(path, &st); err != nil {
		return 0, 0, err
	}
	return uint32(st.Ino), int64(st.Birthtimespec.Sec), nil
}
