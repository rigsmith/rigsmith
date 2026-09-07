package engine

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows cannot hard-link directory symlinks. Rename the reparse point by
// handle, without REPLACE_IF_EXISTS, within its rooted, pinned parent directory.
func installRestoreLink(root *os.Root, temp, name string) error {
	parent, err := root.Open(filepath.Dir(name))
	if err != nil {
		return err
	}
	defer parent.Close()
	objectName, err := windows.NewNTUnicodeString(filepath.Base(temp))
	if err != nil {
		return err
	}
	attrs := windows.OBJECT_ATTRIBUTES{
		RootDirectory: windows.Handle(parent.Fd()), ObjectName: objectName,
		Attributes: windows.OBJ_CASE_INSENSITIVE,
	}
	attrs.Length = uint32(unsafe.Sizeof(attrs))
	var handle windows.Handle
	var status windows.IO_STATUS_BLOCK
	err = windows.NtCreateFile(&handle, windows.DELETE|windows.SYNCHRONIZE, &attrs, &status,
		nil, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		windows.FILE_OPEN, windows.FILE_OPEN_REPARSE_POINT|windows.FILE_OPEN_FOR_BACKUP_INTENT|windows.FILE_SYNCHRONOUS_IO_NONALERT, 0, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)
	newName, err := windows.UTF16FromString(filepath.Base(name))
	if err != nil {
		return err
	}
	// FILE_RENAME_INFORMATION has a variable-length UTF-16 filename. Zero
	// ReplaceIfExists is essential: a concurrent destination must survive.
	type renameInformation struct {
		ReplaceIfExists uint32
		RootDirectory   windows.Handle
		FileNameLength  uint32
		FileName        [1]uint16
	}
	var layout renameInformation
	size := int(unsafe.Offsetof(layout.FileName)) + (len(newName)-1)*2
	buffer := make([]byte, max(size, int(unsafe.Sizeof(layout))))
	info := (*renameInformation)(unsafe.Pointer(&buffer[0]))
	info.RootDirectory = windows.Handle(parent.Fd())
	info.FileNameLength = uint32((len(newName) - 1) * 2)
	copy(unsafe.Slice(&info.FileName[0], len(newName)-1), newName)
	err = windows.NtSetInformationFile(handle, &status, &buffer[0], uint32(size), windows.FileRenameInformation)
	runtime.KeepAlive(parent)
	return err
}

// Native rename failures are NTSTATUS values; normalize them for the same
// collision/unsupported policy as os.Root.Symlink's Win32 errors.
func skipRestoreLinkError(err error) bool {
	var status windows.NTStatus
	if errors.As(err, &status) {
		err = status.Errno()
	}
	return errors.Is(err, os.ErrExist) || errors.Is(err, errors.ErrUnsupported) ||
		errors.Is(err, windows.ERROR_NOT_SUPPORTED) ||
		errors.Is(err, windows.ERROR_CALL_NOT_IMPLEMENTED) ||
		errors.Is(err, windows.ERROR_INVALID_FUNCTION) ||
		errors.Is(err, windows.ERROR_PRIVILEGE_NOT_HELD)
}
