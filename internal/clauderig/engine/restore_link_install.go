//go:build !windows

package engine

import (
	"errors"
	"os"
	"syscall"
)

// Root.Link links the symlink itself and fails atomically if name exists.
func installRestoreLink(root *os.Root, temp, name string) error {
	return root.Link(temp, name)
}

// Unsupported symlinks/hard links and a concurrent destination are normal skips.
func skipRestoreLinkError(err error) bool {
	return errors.Is(err, os.ErrExist) || errors.Is(err, errors.ErrUnsupported) ||
		errors.Is(err, syscall.ENOSYS) || errors.Is(err, syscall.EOPNOTSUPP)
}
