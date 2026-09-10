//go:build windows

package service

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestQueueWindowsVolumeGUIDFallback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	calls := 0
	resolved, err := queueFinalWindowsPath(windows.Handle(f.Fd()), func(h windows.Handle, out *uint16, size, flags uint32) (uint32, error) {
		calls++
		if flags == 0 {
			return 0, windows.ERROR_PATH_NOT_FOUND
		}
		if flags != 1 {
			t.Fatalf("unexpected volume format %d", flags)
		}
		return windows.GetFinalPathNameByHandle(h, out, size, flags)
	})
	if err != nil || calls != 2 || !filepath.IsAbs(resolved) || !strings.HasPrefix(resolved, `\\?\Volume{`) {
		t.Fatal("invalid volume GUID fallback", resolved, calls, err)
	}
	reopened, err := os.Open(resolved)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	before, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	after, err := reopened.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("GUID path selected another file")
	}
	calls = 0
	_, err = queueFinalWindowsPath(windows.Handle(f.Fd()), func(windows.Handle, *uint16, uint32, uint32) (uint32, error) {
		calls++
		return 0, windows.ERROR_ACCESS_DENIED
	})
	if !errors.Is(err, windows.ERROR_ACCESS_DENIED) || calls != 1 {
		t.Fatal("masked permission failure", calls, err)
	}
}
