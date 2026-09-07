//go:build !windows

package engine

import "os"

// Root.Link links the symlink itself and fails atomically if name exists.
func installRestoreLink(root *os.Root, temp, name string) error {
	return root.Link(temp, name)
}
