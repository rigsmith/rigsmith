package cli

import (
	keybind "github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/huh"
)

// huhEscKeyMap is huh's default keymap with esc (and ctrl+c) bound to quit, so
// every one-shot picker can be backed out of with escape — huh's standalone
// field default only quits on ctrl+c, leaving esc inert.
func huhEscKeyMap() *huh.KeyMap {
	km := huh.NewDefaultKeyMap()
	km.Quit = keybind.NewBinding(keybind.WithKeys("esc", "ctrl+c"))
	return km
}

// runHuhSelect runs a single select wrapped in a form so esc cancels it. It
// returns the form error (huh.ErrUserAborted on esc/ctrl+c), so callers treat
// any non-nil error as "cancelled".
func runHuhSelect[T comparable](sel *huh.Select[T]) error {
	return huh.NewForm(huh.NewGroup(sel)).WithKeyMap(huhEscKeyMap()).WithTheme(rigTheme()).Run()
}

// runHuhMultiSelect runs a single multi-select with esc-to-cancel. huh's
// default keymap already provides ctrl+a (select all/none, a toggle) and `/`
// (filter), which coexist; advertise them in the field title where useful.
func runHuhMultiSelect[T comparable](ms *huh.MultiSelect[T]) error {
	return huh.NewForm(huh.NewGroup(ms)).WithKeyMap(huhEscKeyMap()).WithTheme(rigTheme()).Run()
}

// runHuhConfirm runs a single yes/no confirm with esc-to-cancel. The error is
// non-nil on esc/ctrl+c (huh.ErrUserAborted), which callers treat as "declined".
func runHuhConfirm(c *huh.Confirm) error {
	return huh.NewForm(huh.NewGroup(c)).WithKeyMap(huhEscKeyMap()).WithTheme(rigTheme()).Run()
}
