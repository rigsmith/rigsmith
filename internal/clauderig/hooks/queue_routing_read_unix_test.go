//go:build darwin || linux

package hooks

import (
	"path/filepath"
	"syscall"
	"testing"
)

func TestRoutingSettingsRefusesFIFO(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	// No writer exists: opening the FIFO for a read would hang.
	if err := CheckSyncRouting(t.Context(), path); err == nil {
		t.Fatal("accepted non-regular settings")
	}
}
