//go:build darwin || linux

package adapter

import (
	"path/filepath"
	"syscall"
	"testing"
)

func TestInventoryDoesNotOpenFIFO(t *testing.T) {
	root := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(root, "config.toml"), 0600); err != nil {
		t.Fatal(err)
	}
	// No writer exists; a content read would block. Non-regular candidates drop.
	got, err := Inspect(t.Context(), Root{CodexHome, root})
	if err != nil || len(got.Candidates) != 0 {
		t.Fatal(got, err)
	}
}
