package process

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"syscall"
)

func scopeFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 257))
	if err != nil {
		return "", err
	}
	s := strings.TrimSpace(string(b))
	if len(b) > 256 || s == "" {
		return "", ErrRecoveryScope
	}
	return s, nil
}

func currentScope() (recoveryScope, error) {
	host, err := scopeFile("/etc/machine-id")
	if err != nil {
		return recoveryScope{}, err
	}
	boot, err := scopeFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return recoveryScope{}, err
	}
	host, err = normalizeScopeID(host)
	if err != nil {
		return recoveryScope{}, err
	}
	boot, err = normalizeScopeID(boot)
	if err != nil {
		return recoveryScope{}, err
	}
	// A procfs mounted from a different PID namespace cannot prove absence in
	// this process's namespace. Require self to resolve to our actual getpid.
	self, err := os.Readlink("/proc/self")
	if err != nil || self != strconv.Itoa(os.Getpid()) {
		return recoveryScope{}, ErrRecoveryScope
	}
	var ns syscall.Stat_t
	if err := syscall.Stat("/proc/self/ns/pid", &ns); err != nil {
		return recoveryScope{}, err
	}
	return recoveryScope{Host: digest(host), Boot: digest(boot), Namespace: digest(fmt.Sprintf("%d:%d", ns.Dev, ns.Ino))}, nil
}
