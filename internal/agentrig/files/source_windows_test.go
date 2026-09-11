//go:build windows

package files

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestWindowsSourceOpenIsReadOnlyAndNotInherited(t *testing.T) {
	root, s := sourceFixture(t)
	if err := os.WriteFile(filepath.Join(root, "config"), []byte("contents"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := openSourceFile(s.root, "config")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil || string(data) != "contents" {
		t.Fatalf("read failed: %v", err)
	}
	if _, err := f.Write([]byte("write")); err == nil {
		t.Fatal("capture handle allowed writing")
	}
	var flags uint32
	getInfo := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetHandleInformation")
	if ok, _, err := getInfo.Call(f.Fd(), uintptr(unsafe.Pointer(&flags))); ok == 0 {
		t.Fatal(err)
	}
	if flags&windows.HANDLE_FLAG_INHERIT != 0 {
		t.Fatal("source handle is inheritable")
	}
}

func TestWindowsSourceOpenRejectsLinksAndDirectories(t *testing.T) {
	root, s := sourceFixture(t)
	if err := os.Mkdir(filepath.Join(root, "directory"), 0o700); err != nil {
		t.Fatal(err)
	}
	if f, err := openSourceFile(s.root, "directory"); err == nil {
		f.Close()
		t.Fatal("opened directory")
	}
	if err := os.WriteFile(filepath.Join(root, "target"), []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target", filepath.Join(root, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	// Directly exercise the helper as if a regular file had just become a link
	// after Source.Read's initial Lstat. No target bytes may be opened for reading.
	if f, err := openSourceFile(s.root, "link"); err == nil {
		f.Close()
		t.Fatal("opened internal symlink")
	}
}
