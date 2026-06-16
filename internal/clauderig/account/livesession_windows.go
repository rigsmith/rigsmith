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
