//go:build !darwin

package account

import "runtime"

// Desktop account switching is macOS-only for now, and says so rather than
// half-working.
//
// The mechanism is not macOS-specific in principle — Desktop stores the same two
// artifacts on Windows and Linux, sealed with that platform's safeStorage key —
// but two pieces were never verified off macOS and both fail SILENTLY if
// assumed:
//
//   - The cookie swap shells out to sqlite3. There is no sqlite3 on a standard
//     Windows install, so capture would fail and `account add` would quietly
//     record no Desktop session at all.
//   - Detecting a running Desktop is what stops a write landing underneath the
//     live app. The macOS check matches the .app bundle path, which no Linux
//     process has, so the guard would report "not running" every time and the
//     write would be silently lost.
//
// Guessing at either would ship a feature that appears to work and doesn't, so
// they are refused explicitly until someone can verify them on the platform.
const platformDesktopSupported = false

// platformSQLite is where sqlite3 lives when there is a standard location. Set
// even though the feature is gated off here, so the mechanical parts stay
// exercisable by the test suite on Linux CI rather than only on macOS.
var platformSQLite = func() string {
	if runtime.GOOS == "windows" {
		return "" // no standard install location
	}
	return "/usr/bin/sqlite3"
}()

// DesktopRunning reports false: with no verified way to detect the app here, the
// guard would be decorative, and the platform check refuses the operation first.
func DesktopRunning() bool { return false }
