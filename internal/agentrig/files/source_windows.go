//go:build windows

package files

import (
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

func openSourceFile(root *os.Root, name string) (*os.File, error) {
	if !filepath.IsLocal(name) || name == "." || strings.ContainsAny(name, "/\\:") {
		return nil, ErrSource
	}
	dir, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	objectName, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return nil, ErrSource
	}
	attrs := windows.OBJECT_ATTRIBUTES{
		RootDirectory: windows.Handle(dir.Fd()), ObjectName: objectName,
		Attributes: windows.OBJ_CASE_INSENSITIVE | windows.OBJ_DONT_REPARSE,
	}
	attrs.Length = uint32(unsafe.Sizeof(attrs))
	var handle windows.Handle
	var status windows.IO_STATUS_BLOCK
	// Unlike Root.Open, this opens only the direct child of the pinned directory
	// and never resolves a reparse point. No handle-inherit attribute is set.
	// Complete-if-oplocked avoids waiting for another process to release an oplock;
	// an alternate status is refused and any returned handle is closed below.
	err = windows.NtCreateFile(&handle, windows.FILE_GENERIC_READ, &attrs, &status, nil, 0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		windows.FILE_OPEN, windows.FILE_NON_DIRECTORY_FILE|windows.FILE_OPEN_REPARSE_POINT|
			windows.FILE_SYNCHRONOUS_IO_NONALERT|windows.FILE_COMPLETE_IF_OPLOCKED|windows.FILE_OPEN_NO_RECALL, 0, 0)
	if err != nil {
		if handle != 0 && handle != windows.InvalidHandle {
			windows.CloseHandle(handle)
		}
		if nt, ok := err.(windows.NTStatus); ok {
			return nil, nt.Errno()
		}
		return nil, err
	}
	typ, err := windows.GetFileType(handle)
	if err != nil || typ != windows.FILE_TYPE_DISK {
		windows.CloseHandle(handle)
		return nil, ErrSource
	}
	var info windows.ByHandleFileInformation
	err = windows.GetFileInformationByHandle(handle, &info)
	if err != nil || info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY|windows.FILE_ATTRIBUTE_OFFLINE) != 0 {
		windows.CloseHandle(handle)
		return nil, ErrSource
	}
	return os.NewFile(uintptr(handle), "source"), nil
}
