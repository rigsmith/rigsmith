// Package assets embeds the tray icons and resolves the one matching a health
// level. Two variants per state — the menu bar can be light or dark, and the
// brand palette carries a value for each.
package assets

import (
	_ "embed"
	"runtime"

	"github.com/rigsmith/rigsmith/internal/clauderig/health"
)

// The claudeRig mark (brackets + spark) inked in the brand's ok/warn/error
// colours. See README.md for the source SVG and how to regenerate.
var (
	//go:embed tray-green-light.png
	trayGreenLight []byte
	//go:embed tray-green-dark.png
	trayGreenDark []byte
	//go:embed tray-amber-light.png
	trayAmberLight []byte
	//go:embed tray-amber-dark.png
	trayAmberDark []byte
	//go:embed tray-red-light.png
	trayRedLight []byte
	//go:embed tray-red-dark.png
	trayRedDark []byte
)

// The same marks at 32px. Windows draws the notification area icon at 16px
// (32 on a high-DPI display) and scales whatever it is handed to fit — from 44
// that is a non-integer ratio, and the mark came out visibly squashed. The
// README used to claim 44 "downsamples cleanly" on Windows; it does not.
var (
	//go:embed tray-green-light-32.png
	trayGreenLight32 []byte
	//go:embed tray-green-dark-32.png
	trayGreenDark32 []byte
	//go:embed tray-amber-light-32.png
	trayAmberLight32 []byte
	//go:embed tray-amber-dark-32.png
	trayAmberDark32 []byte
	//go:embed tray-red-light-32.png
	trayRedLight32 []byte
	//go:embed tray-red-dark-32.png
	trayRedDark32 []byte
)

// Tray returns the icon for lvl: the pair is (light-background, dark-background),
// matching SetIcon / SetDarkModeIcon. An unknown level falls back to amber —
// "something is off" is the honest reading of a state we cannot name.
func Tray(lvl health.Level) (light, dark []byte) {
	// 44px is the macOS retina menu bar slot (22pt @2x); Windows wants 32.
	// Handing each platform the size it actually draws is the difference
	// between a crisp mark and a scaled one.
	if runtime.GOOS == "windows" {
		switch lvl {
		case health.Green:
			return trayGreenLight32, trayGreenDark32
		case health.Red:
			return trayRedLight32, trayRedDark32
		default:
			return trayAmberLight32, trayAmberDark32
		}
	}
	switch lvl {
	case health.Green:
		return trayGreenLight, trayGreenDark
	case health.Red:
		return trayRedLight, trayRedDark
	default:
		return trayAmberLight, trayAmberDark
	}
}
