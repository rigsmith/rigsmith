package commands

import (
	keybind "github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/huh"
	"github.com/rigsmith/rigsmith/core/brand"
)

// huhEscKeyMap is huh's default keymap with esc (and ctrl+c) bound to quit, so
// every wizard/prompt can be backed out of with escape. huh's default only
// quits on ctrl+c, leaving esc inert. Mirrors the other rig tools so escape
// behaves the same everywhere.
func huhEscKeyMap() *huh.KeyMap {
	km := huh.NewDefaultKeyMap()
	km.Quit = keybind.NewBinding(keybind.WithKeys("esc", "ctrl+c"))
	return km
}

// confirm asks a yes/no question in the brand theme. It is only ever reached
// behind an interactive() check: a prompt a script hits blocks on stdin forever,
// which is why every destructive command refuses rather than prompts when there
// is no terminal.
func confirm(title string) (bool, error) {
	var ok bool
	form := brand.ConfirmForm(brand.AccentCodex, title, "Yes", "No", &ok).WithKeyMap(huhEscKeyMap())
	if err := form.Run(); err != nil {
		return false, err
	}
	return ok, nil
}
