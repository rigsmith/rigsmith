package brand

import (
	keybind "github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
)

// ConfirmForm builds a compact yes/no confirm for embedding inside a running
// Bubble Tea program — the same huh.Confirm widget the standalone pickers use
// (two highlighted buttons, arrow/tab to move, y/n shortcuts), painted with the
// given tool accent so an inline gate matches the rest of the brand.
//
// Drive it via the parent model's Update/View; do NOT call Run. Because Run is
// what arms huh's submit/cancel quit commands, an embedded form returns a nil
// command when it finishes: watch its State for StateCompleted (read the bound
// *bool) or StateAborted (esc / ctrl+c → treat as "no"), and never propagate the
// form's command at those terminal states. Help is hidden to keep it to a few
// lines; the Yes/No buttons are self-evident in a footer.
func ConfirmForm(accent lipgloss.AdaptiveColor, title, affirmative, negative string, v *bool) *huh.Form {
	return huh.NewForm(huh.NewGroup(
		huh.NewConfirm().
			Title(title).
			Affirmative(affirmative).
			Negative(negative).
			Value(v),
	)).WithTheme(Theme(accent)).WithKeyMap(confirmKeyMap()).WithShowHelp(false)
}

// confirmKeyMap is huh's default keymap with esc (and ctrl+c) bound to quit, so
// an embedded confirm can be backed out of with escape — matching the esc-cancel
// behaviour of the standalone pickers across the rig tools.
func confirmKeyMap() *huh.KeyMap {
	km := huh.NewDefaultKeyMap()
	km.Quit = keybind.NewBinding(keybind.WithKeys("esc", "ctrl+c"))
	return km
}
