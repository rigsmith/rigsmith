//go:build windows

package commands

import (
	"fmt"
	"golang.org/x/sys/windows"
	"os"
)

func validateQueueRequestSingleLink(f *os.File) error {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(f.Fd()), &info); err != nil {
		return err
	}
	if info.NumberOfLinks != 1 {
		return fmt.Errorf("queue request must have exactly one hard link")
	}
	return nil
}

// Like the queue runtime and manual producer files, Windows inbox privacy is
// a caller-provided ACL prerequisite. This checks no ACL and promises no repair.
func queueInboxPrivate(info os.FileInfo) bool { return true }
