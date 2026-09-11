//go:build linux || darwin

package commands

import (
	"fmt"
	"os"
	"syscall"
)

func validateQueueRequestSingleLink(f *os.File) error {
	info, err := f.Stat()
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Nlink != 1 {
		return fmt.Errorf("queue request must have exactly one hard link")
	}
	return nil
}

func queueInboxPrivate(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Geteuid()) && info.Mode().Perm()&0077 == 0
}
