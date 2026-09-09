package process

import (
	"encoding/hex"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

func currentScope() (recoveryScope, error) {
	var host [16]byte
	timeout := syscall.Timespec{Sec: 1}
	_, _, errno := syscall.Syscall(syscall.SYS_GETHOSTUUID, uintptr(unsafe.Pointer(&host[0])), uintptr(unsafe.Pointer(&timeout)), 0)
	if errno != 0 {
		return recoveryScope{}, errno
	}
	if host == [16]byte{} {
		return recoveryScope{}, ErrRecoveryScope
	}
	boot, err := unix.Sysctl("kern.bootsessionuuid")
	if err != nil || boot == "" {
		return recoveryScope{}, ErrRecoveryScope
	}
	boot, err = normalizeScopeID(boot)
	if err != nil {
		return recoveryScope{}, err
	}
	return recoveryScope{Host: digest(hex.EncodeToString(host[:])), Boot: digest(boot), Namespace: digest("darwin-host-processes")}, nil
}
