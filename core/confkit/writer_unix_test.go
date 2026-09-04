//go:build !windows

package confkit

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// A fresh config takes the process's umask, as any file os.WriteFile creates
// would; an existing file keeps the mode it had, however the umask is set.
func TestWriterFreshFileHonoursUmask(t *testing.T) {
	old := syscall.Umask(0o077)
	defer syscall.Umask(old)
	dir := t.TempDir()
	w := Writer{}
	fresh := filepath.Join(dir, "fresh.jsonc")
	if !w.SetBool(fresh, []string{"a"}, true) {
		t.Fatal("Set failed")
	}
	if fi, err := os.Stat(fresh); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("fresh file mode = %v, %v; want 0600 under umask 077", fi.Mode().Perm(), err)
	}
	kept := filepath.Join(dir, "kept.jsonc")
	if err := os.WriteFile(kept, []byte("{\n  \"a\": 1\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(kept, 0o644); err != nil {
		t.Fatal(err)
	}
	if !w.SetBool(kept, []string{"a"}, false) {
		t.Fatal("Set failed")
	}
	if fi, err := os.Stat(kept); err != nil || fi.Mode().Perm() != 0o644 {
		t.Fatalf("existing file mode = %v, %v; want 0644 kept", fi.Mode().Perm(), err)
	}
}
