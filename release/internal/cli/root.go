// Package cli defines relrig's command surface. relrig is the release front
// door: it re-exposes the full changeset lifecycle (init/add/status/version/info)
// from the shared changerig/commands package and adds the release-orchestration
// verbs (publish, tag, pre) on top.
package cli

import (
	"context"

	"github.com/charmbracelet/fang"
	"github.com/rigsmith/changerig/commands"
	"github.com/rigsmith/core/brand"
	"github.com/spf13/cobra"
)

// Execute builds the command tree and runs it through fang.
func Execute(ctx context.Context) error {
	return fang.Execute(ctx, newRootCmd(), fang.WithColorSchemeFunc(brand.ColorSchemeFunc(brand.AccentShip)))
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "relrig",
		Short:         "Uniform changeset → version → publish, across every ecosystem",
		Long:          "relrig manages the whole release: it captures changesets, versions packages\nwith the shared engine (the same one changerig uses), and publishes via the\nnative package managers. One front door for .NET, Node, Go, and Rust.",
		SilenceUsage:  true,
		SilenceErrors: false,
	}

	add := commands.NewAddCmd()
	// Bare `relrig` behaves like `relrig add`.
	root.RunE = add.RunE
	root.Args = add.Args
	root.Flags().AddFlagSet(add.Flags())

	root.AddCommand(
		commands.NewInitCmd(),
		add,
		commands.NewStatusCmd(),
		commands.NewVersionCmd(),
		commands.NewInfoCmd(),
		commands.NewConfigCmd(),
		commands.NewUICmd(),
		commands.NewPreCmd(),
		newPublishCmd(),
		newTagCmd(),
		newReleaseCmd(),
	)
	return root
}
