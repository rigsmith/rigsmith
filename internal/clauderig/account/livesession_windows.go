//go:build windows

package account

import "golang.org/x/sys/windows"

// pidAlive reports whether a process with the given pid currently exists by
// opening it with minimal rights. A successful open (handle closed immediately)
// means the process is present.
func pidAlive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	windows.CloseHandle(h)
	return true
}

// Windows has no graceful per-process signal equivalent for GUI apps that's
// reliable from another process, so both paths use TerminateProcess.
func terminate(pid int) error { return killWindows(pid) }
func forceKill(pid int) error { return killWindows(pid) }

func killWindows(pid int) error {
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	return windows.TerminateProcess(h, 1)
}
