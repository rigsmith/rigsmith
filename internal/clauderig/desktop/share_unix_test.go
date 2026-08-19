//go:build !windows

package desktop

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// An entry that cannot be copied at all must stop the share BEFORE the source
// directory is removed — otherwise "nothing is destroyed" is false.
func TestShareRefusesRatherThanDestroyAnUnmovableEntry(t *testing.T) {
	_, p, root := shareFixture(t)
	own := filepath.Join(p.DataDir(), "claude-code-sessions")
	writeFile(t, filepath.Join(own, "acct-a", "s1.json"), "history")
	// A fifo stands in for the general case: an entry that is neither a regular
	// file nor a symlink, so it cannot be copied at all. Unix-only by build tag —
	// syscall.Mkfifo does not exist on Windows, so a runtime guard would still
	// fail to compile there.
	sock := filepath.Join(own, "acct-a", "live.fifo")
	if err := syscall.Mkfifo(sock, 0o600); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}

	if _, serr := Share(p, root, SharedDirs); serr == nil {
		t.Fatal("Share succeeded despite an entry it cannot migrate")
	}
	// The profile's directory — and the socket — are untouched.
	if _, serr := os.Lstat(sock); serr != nil {
		t.Fatalf("the unmovable entry was destroyed: %v", serr)
	}
	if got := readFile(t, filepath.Join(own, "acct-a", "s1.json")); got != "history" {
		t.Fatalf("the source directory was disturbed: %q", got)
	}
}
