package files

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

func lockReplacement(f *os.File) (bool, error) {
	err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{})
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return false, nil
	}
	return err == nil, err
}

// File contents are flushed before installation. Windows does not support the
// Unix directory-fsync contract here; no power-loss durability claim is made.
func syncReplacementDirectory(*os.Root) error { return nil }
