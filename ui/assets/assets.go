// Package assets embeds the tray icons and resolves the one matching a health
// level. Two variants per state — the menu bar can be light or dark, and the
// brand palette carries a value for each.
package assets

import (
	_ "embed"

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

// Tray returns the icon for lvl: the pair is (light-background, dark-background),
// matching SetIcon / SetDarkModeIcon. An unknown level falls back to amber —
// "something is off" is the honest reading of a state we cannot name.
func Tray(lvl health.Level) (light, dark []byte) {
	switch lvl {
	case health.Green:
		return trayGreenLight, trayGreenDark
	case health.Red:
		return trayRedLight, trayRedDark
	default:
		return trayAmberLight, trayAmberDark
	}
}
