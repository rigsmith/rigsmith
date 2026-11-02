package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// EvalSymlinks also expands Windows 8.3 names and normalizes existing path
// components. Resolve before Rel or handing the member prefix to MSBuild.
func stackGuardPath(path string) string {
	if path == "" {
		return ""
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	if resolved, err := filepath.EvalSymlinks(absolute); err == nil {
		return resolved
	}
	return absolute
}

func stackGuardSamePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	left, leftErr := os.Stat(a)
	right, rightErr := os.Stat(b)
	if leftErr == nil && rightErr == nil {
		return os.SameFile(left, right)
	}
	// Do not guess an identity when only one spelling resolves on disk.
	if leftErr == nil || rightErr == nil {
		return false
	}
	a, b = stackGuardPath(a), stackGuardPath(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// Compare actual ancestor directories, rather than spelling prefixes: the
// reported path can use a different drive case, long name or symlink spelling.
// Missing paths never qualify for the sibling-warning exemption.
func stackGuardWithin(root, path string) bool {
	if root == "" || path == "" {
		return false
	}
	parent, err := os.Stat(root)
	if err != nil || !parent.IsDir() {
		return false
	}
	current := stackGuardPath(path)
	for {
		info, err := os.Stat(current)
		if err != nil {
			return false
		}
		if os.SameFile(parent, info) {
			return true
		}
		next := filepath.Dir(current)
		if next == current {
			return false
		}
		current = next
	}
}
