//go:build windows

package service

import (
	"errors"
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
	return queueFinalWindowsPath(handle, windows.GetFinalPathNameByHandle)
}

// The query argument lets the Windows regression force DOS-name lookup failure
// while still resolving the same real file handle with the native GUID query.
func queueFinalWindowsPath(handle windows.Handle, query func(windows.Handle, *uint16, uint32, uint32) (uint32, error)) (string, error) {
	// FILE_NAME_NORMALIZED | VOLUME_NAME_DOS are both zero. The final name uses
	// the extended DOS/UNC prefix; normalize it to the spelling used by our paths.
	buffer := make([]uint16, 32768)
	n, err := query(handle, &buffer[0], uint32(len(buffer)), 0)
	if errors.Is(err, windows.ERROR_PATH_NOT_FOUND) {
		// A local volume without a DOS drive name can still have a stable GUID.
		n, err = query(handle, &buffer[0], uint32(len(buffer)), 1) // VOLUME_NAME_GUID
	}
	if err != nil {
		return "", err
	}
	if n == 0 || n >= uint32(len(buffer)) {
		return "", fmt.Errorf("queue path exceeds Windows path limit")
	}
	resolved := windows.UTF16ToString(buffer[:n])
	if strings.HasPrefix(resolved, `\\?\UNC\`) {
		resolved = `\\` + strings.TrimPrefix(resolved, `\\?\UNC\`)
	} else if !strings.HasPrefix(resolved, `\\?\Volume{`) {
		resolved = strings.TrimPrefix(resolved, `\\?\`)
	}
	if !filepath.IsAbs(resolved) {
		return "", fmt.Errorf("Windows returned a nonabsolute queue path")
	}
	return filepath.Clean(resolved), nil
}
