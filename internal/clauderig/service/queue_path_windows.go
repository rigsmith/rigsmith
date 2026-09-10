//go:build windows

package service

import (
	"fmt"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// Go's EvalSymlinks does not resolve directory junctions. Open the existing
// target with normal reparse processing, then ask Windows for its final path.
// A broken junction fails the open instead of becoming a missing path suffix.
func queueResolveExistingPath(path string) (string, error) {
	// Use the extended absolute spelling so native opens retain long-path support.
	if !strings.HasPrefix(path, `\\?\`) {
		if strings.HasPrefix(path, `\\`) {
			path = `\\?\UNC\` + strings.TrimPrefix(path, `\\`)
		} else {
			path = `\\?\` + path
		}
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	handle, err := windows.CreateFile(name, 0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(handle)
	// FILE_NAME_NORMALIZED | VOLUME_NAME_DOS are both zero. The final name uses
	// the extended DOS/UNC prefix; normalize it to the spelling used by our paths.
	buffer := make([]uint16, 32768)
	n, err := windows.GetFinalPathNameByHandle(handle, &buffer[0], uint32(len(buffer)), 0)
	if err != nil {
		return "", err
	}
	if n == 0 || n >= uint32(len(buffer)) {
		return "", fmt.Errorf("queue path exceeds Windows path limit")
	}
	resolved := windows.UTF16ToString(buffer[:n])
	if strings.HasPrefix(resolved, `\\?\UNC\`) {
		resolved = `\\` + strings.TrimPrefix(resolved, `\\?\UNC\`)
	} else {
		resolved = strings.TrimPrefix(resolved, `\\?\`)
	}
	if !filepath.IsAbs(resolved) {
		return "", fmt.Errorf("Windows returned a nonabsolute queue path")
	}
	return filepath.Clean(resolved), nil
}
