//go:build linux || darwin

package commands

import (
	"fmt"
	"os"
	"syscall"
)

func queueRequestSingleLink(f *os.File) error {
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
