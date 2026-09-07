package process

import (
	"golang.org/x/sys/unix"
	"syscall"
	"unsafe"
)

func waitExited(pid int) error {
	// Darwin waitid(P_PID, pid, siginfo, WEXITED|WNOWAIT). Only the exit
	// notification is needed; aligned storage exceeds Darwin's siginfo_t size.
	var info [32]uint64
	for {
		_, _, errno := syscall.Syscall6(syscall.SYS_WAITID, 1, uintptr(pid), uintptr(unsafe.Pointer(&info[0])), syscall.WEXITED|syscall.WNOWAIT, 0, 0)
		if errno == syscall.EINTR {
			continue
		}
		if errno != 0 {
			return errno
		}
		return nil
	}
}

func groupAlive(pid int) (bool, error) {
	members, err := unix.SysctlKinfoProcSlice("kern.proc.pgrp", pid)
	if err != nil {
		return false, err
	}
	for _, member := range members {
		if member.Proc.P_stat != 5 {
			return true, nil
		}
	} // SZOMB
	return false, nil
}
