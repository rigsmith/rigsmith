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

// terminate asks a process to exit gracefully (SIGTERM), letting editors like
// VS Code save and shut down cleanly.
func terminate(pid int) error { return syscall.Kill(pid, syscall.SIGTERM) }

// forceKill ends a process immediately (SIGKILL) — used only for stragglers that
// ignore SIGTERM within the grace period.
func forceKill(pid int) error { return syscall.Kill(pid, syscall.SIGKILL) }
