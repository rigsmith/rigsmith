//go:build windows

package account

import (
	"errors"
	"fmt"
	"math"

	"golang.org/x/sys/windows"
)

// A Windows pid is a DWORD. A lock file can hold any int, and uint32(pid) of a
// value past MaxUint32 wraps to a DIFFERENT pid — for terminate, an unrelated
// process. Out of range means "no such process", never "some other process".
func fitsDWORD(pid int) bool { return pid > 0 && pid <= math.MaxUint32 }

func pidAlive(pid int) bool {
	if !fitsDWORD(pid) {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		// Only "there is no such process" means dead; access-denied means it
		// is there and not ours to inspect.
		return !errors.Is(err, windows.ERROR_INVALID_PARAMETER)
	}
	defer windows.CloseHandle(h)
	return true
}

func terminate(pid int, hard bool) error {
	if !fitsDWORD(pid) {
		return fmt.Errorf("pid %d is not a valid Windows process id", pid)
	}
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	return windows.TerminateProcess(h, 1)
}
