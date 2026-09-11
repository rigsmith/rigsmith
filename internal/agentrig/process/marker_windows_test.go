package process

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// These helper markers are published by rename. Seeing the new name does not
// guarantee a reader can open it yet: Windows can briefly retain an incompatible
// handle during publication. Wait for readable bytes within the existing startup
// deadline, retrying only missing files and sharing violations. Parse/contents
// errors remain the caller's responsibility and are never retried.
func readWindowsMarker(path string, timeout time.Duration) ([]byte, error) {
	deadline := time.Now().Add(timeout)
	for {
		data, err := os.ReadFile(path)
		if err == nil || (!errors.Is(err, os.ErrNotExist) && !errors.Is(err, windows.ERROR_SHARING_VIOLATION)) || !time.Now().Before(deadline) {
			return data, err
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestWindowsMarkerSharingViolation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "marker")
	if err := os.WriteFile(path, []byte("123"), 0600); err != nil {
		t.Fatal(err)
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if handle != windows.InvalidHandle {
			windows.CloseHandle(handle)
		}
	}()
	// An actual incompatible handle must remain an error if it outlives readiness.
	if _, err := readWindowsMarker(path, 25*time.Millisecond); !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		t.Fatal("held marker was accepted", err)
	}
	type result struct {
		data []byte
		err  error
	}
	done := make(chan result, 1)
	go func() { data, err := readWindowsMarker(path, time.Second); done <- result{data, err} }()
	select {
	case r := <-done:
		t.Fatal("reader did not wait for sharing handle", r.err)
	case <-time.After(25 * time.Millisecond):
	}
	if err := windows.CloseHandle(handle); err != nil {
		t.Fatal(err)
	}
	handle = windows.InvalidHandle
	select {
	case r := <-done:
		if r.err != nil || string(r.data) != "123" {
			t.Fatal("marker not read after handle closed", r.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reader did not finish")
	}
	// A non-transient error must remain identifiable, and missing markers time out.
	if _, err := readWindowsMarker("\x00", time.Second); err == nil || errors.Is(err, os.ErrNotExist) || errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		t.Fatal("invalid name error changed", err)
	}
	if _, err := readWindowsMarker(path+".missing", 25*time.Millisecond); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("missing marker was accepted", err)
	}
}
