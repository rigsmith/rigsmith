package queue

import (
	"os"

	"golang.org/x/sys/windows"
)

func syncData(f *os.File) error { return f.Sync() } // FlushFileBuffers
func replaceFile(from, to string) error {
	src, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	dst, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(src, dst, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}

// Windows publishes with write-through above; Unix-style directory fsync is not
// available through os.File. Power-loss behavior still depends on the filesystem
// and storage device honoring flush requests; process-crash recovery is tested.
func syncDirectory(string) error { return nil }
