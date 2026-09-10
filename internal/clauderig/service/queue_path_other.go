//go:build !windows

package service

import "path/filepath"

func queueResolveExistingPath(path string) (string, error) {
	return filepath.EvalSymlinks(path)
}
