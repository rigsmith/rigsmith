//go:build windows

package account

import (
	"os/exec"
	"strings"
)

// DesktopRunning reports whether Claude Desktop is currently open. Writing the
// session underneath a running Desktop is silently lost: it holds the Cookies DB
// open and rewrites config.json on exit.
func DesktopRunning() bool {
	// tasklist is the dependency-free way to ask; its "no tasks" reply still exits
	// zero, so the image name has to be matched in the output rather than trusted
	// from the exit code.
	out, err := exec.Command("tasklist.exe", "/FI", "IMAGENAME eq Claude.exe", "/NH").Output()
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(string(out)), "claude.exe")
}
