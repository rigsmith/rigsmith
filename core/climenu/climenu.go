// Package climenu renders a bare cobra command group as an interactive,
// brand-themed menu. When a user runs a group command (e.g. `rig config`) on a
// TTY with no verb, Run lists the group's subcommands and runs the chosen one.
// It is the shared implementation behind every "bare group → menu" command, so
// the four tools present their groups identically and a new group opts in with a
// one-line RunE rather than a bespoke screen.
package climenu

import (
	keybind "github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/huh"
	"github.com/rigsmith/rigsmith/core/brand"
	"github.com/spf13/cobra"
)

// Run shows cmd's subcommand menu and runs the selection. Only subcommands that
// can run with no positional arguments are offered, so arg-required verbs (like
// `config set <key> <value>`) stay command-line only rather than appearing as
// dead-end menu entries. The accent is resolved from the invoking tool
// (cmd.Root().Name()), so a builder shared between tools — changerig and shiprig
// share NewConfigCmd — still paints in each tool's color. Esc / ctrl+c cancels
// cleanly (no error, no action). With nothing menu-runnable it falls back to the
// group's help.
func Run(cmd *cobra.Command) error {
	opts := options(cmd)
	if len(opts) == 0 {
		return cmd.Help()
	}
	var chosen *cobra.Command
	sel := huh.NewSelect[*cobra.Command]().
		Title(cmd.CommandPath()).
		Description(cmd.Short).
		Options(opts...).
		Value(&chosen)
	if err := huh.NewForm(huh.NewGroup(sel)).
		WithKeyMap(escKeyMap()).
		WithTheme(brand.Theme(brand.AccentFor(cmd.Root().Name()))).
		Run(); err != nil {
		return nil // esc/ctrl+c (huh.ErrUserAborted) → cancelled, a clean no-op
	}
	if chosen == nil {
		return nil
	}
	chosen.SetContext(cmd.Context())
	chosen.SetOut(cmd.OutOrStdout())
	chosen.SetErr(cmd.ErrOrStderr())
	if chosen.RunE != nil {
		return chosen.RunE(chosen, nil)
	}
	if chosen.Run != nil {
		chosen.Run(chosen, nil)
	}
	return nil
}

// options builds one menu entry per menu-runnable subcommand, labelled
// "name — short".
func options(cmd *cobra.Command) []huh.Option[*cobra.Command] {
	var opts []huh.Option[*cobra.Command]
	for _, c := range cmd.Commands() {
		if !menuRunnable(c) {
			continue
		}
		label := c.Name()
		if c.Short != "" {
			label += " — " + c.Short
		}
		opts = append(opts, huh.NewOption(label, c))
	}
	return opts
}

// menuRunnable reports whether c is worth offering in a group menu: it actually
// runs (has a RunE/Run, so it isn't itself a sub-group or cobra's generated
// help/completion command) and accepts zero positional args (so picking it can't
// dead-end on a "requires N args" error).
func menuRunnable(c *cobra.Command) bool {
	if c.Hidden || c.Name() == "help" || c.Name() == "completion" {
		return false
	}
	if c.RunE == nil && c.Run == nil {
		return false
	}
	if c.Args == nil {
		return true
	}
	return c.Args(c, nil) == nil
}

// escKeyMap binds esc (and ctrl+c) to quit so the menu can be backed out of with
// escape, matching the one-shot pickers across the rig tools.
func escKeyMap() *huh.KeyMap {
	km := huh.NewDefaultKeyMap()
	km.Quit = keybind.NewBinding(keybind.WithKeys("esc", "ctrl+c"))
	return km
}
