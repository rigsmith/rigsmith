//go:build darwin

package account

import (
	"os/exec"
	"strings"
)

// desktopProcessMatch is the executable path fragment that identifies the Claude
// Desktop app itself. It is deliberately specific: matching a bare "Claude" would
// also match this very CLI, every `claude` session, and any editor window with
// the word in its command line — and a false positive here blocks switching with
// a confusing "Desktop is running".
// desktopSupported gates the whole feature. Only macOS is verified: the
// capture/restore round-trip, the safeStorage ciphertext behaviour and the
// process match were all confirmed against the real app there. See
// desktop_other.go for why the other platforms are refused rather than guessed.
const platformDesktopSupported = true

// sqlite3Bin is pinned to Apple's absolute path, not resolved via PATH: this
// reads and writes session cookies, so an attacker-controlled `sqlite3` earlier
// on PATH must not intercept them. Present on every macOS.
const platformSQLite = "/usr/bin/sqlite3"

const desktopProcessMatch = "Claude.app/Contents/MacOS/Claude"

// DesktopRunning reports whether Claude Desktop is currently open. Writing the
// session underneath a running Desktop is silently lost: it holds the Cookies DB
// open and rewrites config.json on exit.
func DesktopRunning() bool {
	// pgrep -f matches against the full argv. -x would not work: the app is
	// launched with arguments.
	out, err := exec.Command("/usr/bin/pgrep", "-f", desktopProcessMatch).Output()
	if err != nil {
		return false // non-zero exit means no match
	}
	return strings.TrimSpace(string(out)) != ""
}
