//go:build !windows

package account

import (
	"errors"
	"syscall"
)

func pidAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func terminate(pid int, hard bool) error {
	sig := syscall.SIGTERM
	if hard {
		sig = syscall.SIGKILL
	}
	return syscall.Kill(pid, sig)
}
