package commands

import (
	keybind "github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/huh"
)

// huhEscKeyMap is huh's default keymap with esc (and ctrl+c) bound to quit, so
// every wizard/prompt can be backed out of with escape. huh's default only
// quits on ctrl+c, leaving esc inert. Mirrors the cli tool's helper of the same
// name so escape behaves the same across the rig tools.
func huhEscKeyMap() *huh.KeyMap {
	km := huh.NewDefaultKeyMap()
	km.Quit = keybind.NewBinding(keybind.WithKeys("esc", "ctrl+c"))
	return km
}
