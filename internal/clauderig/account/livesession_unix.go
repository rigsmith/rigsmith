//go:build !windows

package account

import "syscall"

// pidAlive reports whether a process with the given pid currently exists.
// Signal 0 performs error-checking without delivering a signal: nil or EPERM
// (alive but owned by another user) means present; ESRCH means gone.
func pidAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
