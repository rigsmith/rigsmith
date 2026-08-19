//go:build !windows

package desktop

import "os"

// linkDir points link at target. A plain directory symlink is all Unix needs.
func linkDir(target, link string) error { return os.Symlink(target, link) }
