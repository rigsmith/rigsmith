//go:build windows

package desktop

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// linkDir points link at target.
//
// Windows is the reason this is not just os.Symlink: creating a directory
// symlink there requires SeCreateSymbolicLinkPrivilege, which a normal user does
// not have unless Developer Mode is on — so the obvious call fails for most
// people. A JUNCTION does the same job for a local directory and needs no
// privilege at all, which is why `mklink /J` is the primary path.
//
// os.Symlink is still tried first: it produces the more standard artifact when
// the privilege IS available, and junctions do not follow remote paths.
func linkDir(target, link string) error {
	if err := os.Symlink(target, link); err == nil {
		return nil
	}
	// cmd.exe owns mklink — it is a shell builtin, not an executable.
	out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput()
	if err != nil {
		return fmt.Errorf("create junction %s → %s: %w: %s",
			link, target, err, strings.TrimSpace(string(out)))
	}
	return nil
}
