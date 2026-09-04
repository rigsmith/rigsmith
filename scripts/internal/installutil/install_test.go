package installutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrepareDirCreatesWritableDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "bin")
	relaunched, err := PrepareDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if relaunched {
		t.Fatal("a writable temporary directory must not trigger elevation")
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("install directory was not created: %v", err)
	}
}
