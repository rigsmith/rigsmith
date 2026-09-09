package desktop

import (
	"path/filepath"
	"strings"
)

// CanonicalDir normalises a data directory for comparison: symlinks resolved
// where possible, and case folded, so a store entry that is a directory symlink
// or a case-insensitive filesystem cannot make one window look like two.
//
// Exported because two callers now decide profile identity from a running
// instance's --user-data-dir: the routing guard behind `desktop send`, and the
// UI's watch on which Desktop windows are open. Two normalisations that drifted
// apart would have the CLI refuse a send over a window the UI had just called
// closed.
func CanonicalDir(dir string) string {
	if dir == "" {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	return strings.ToLower(filepath.Clean(dir))
}
