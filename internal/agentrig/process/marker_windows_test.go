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
	return readWindowsMarkerWithRead(path, timeout, os.ReadFile)
}

func readWindowsMarkerWithRead(path string, timeout time.Duration, read func(string) ([]byte, error)) ([]byte, error) {
	deadline := time.Now().Add(timeout)
	for {
		data, err := read(path)
		if err == nil || (!errors.Is(err, os.ErrNotExist) && !errors.Is(err, windows.ERROR_SHARING_VIOLATION)) || !time.Now().Before(deadline) {
			return data, err
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestWindowsMarkerTransientReads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "marker")
	if err := os.WriteFile(path, []byte("123"), 0600); err != nil {
		t.Fatal(err)
	}
	sharing := &os.PathError{Op: "open", Path: path, Err: windows.ERROR_SHARING_VIOLATION}
	attempts := 0
	data, err := readWindowsMarkerWithRead(path, time.Second, func(path string) ([]byte, error) {
		attempts++
		if attempts == 1 {
			return nil, sharing
		}
		return os.ReadFile(path)
	})
	if err != nil || string(data) != "123" || attempts < 2 {
		t.Fatal("marker was not retried after sharing violation", attempts, err)
	}
	// A persistent transient error must survive the readiness deadline.
	if _, err := readWindowsMarkerWithRead(path, 25*time.Millisecond, func(string) ([]byte, error) {
		return nil, sharing
	}); !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		t.Fatal("persistent sharing violation was accepted", err)
	}
	// Other failures must return immediately rather than exhaust the deadline.
	attempts = 0
	denied := &os.PathError{Op: "open", Path: path, Err: windows.ERROR_ACCESS_DENIED}
	if _, err := readWindowsMarkerWithRead(path, time.Second, func(string) ([]byte, error) {
		attempts++
		return nil, denied
	}); !errors.Is(err, windows.ERROR_ACCESS_DENIED) || attempts != 1 {
		t.Fatal("non-transient failure was retried or lost", attempts, err)
	}
	// Exercise missing-file recovery with real reads and publication. Publish
	// only after the first failed read, so no goroutine scheduling is involved.
	missing := path + ".missing"
	attempts = 0
	data, err = readWindowsMarkerWithRead(missing, time.Second, func(path string) ([]byte, error) {
		attempts++
		data, err := os.ReadFile(path)
		if attempts == 1 {
			if !errors.Is(err, os.ErrNotExist) {
				t.Fatal("first read did not observe missing marker", err)
			}
			if writeErr := os.WriteFile(path, []byte("456"), 0600); writeErr != nil {
				t.Fatal(writeErr)
			}
		}
		return data, err
	})
	if err != nil || string(data) != "456" || attempts < 2 {
		t.Fatal("newly published marker was not read", attempts, err)
	}
	if _, err := readWindowsMarker(path+".absent", 25*time.Millisecond); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("missing marker was accepted", err)
	}
}
