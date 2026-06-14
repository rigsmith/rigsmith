// Package cli defines shiprig's command surface. shiprig is the release front
// door: it re-exposes the full changeset lifecycle (init/add/status/version/info)
// from the shared changerig/commands package and adds the release-orchestration
// verbs (publish, tag, pre) on top.
package cli

import (
	"context"

	"github.com/rigsmith/changerig/commands"
	"github.com/rigsmith/core/brand"
	"github.com/rigsmith/core/fang"
	"github.com/spf13/cobra"
)

// Execute builds the command tree and runs it through fang.
func Execute(ctx context.Context) error {
	return fang.Execute(ctx, newRootCmd(), fang.WithColorSchemeFunc(brand.ColorSchemeFunc(brand.AccentShip)), fang.WithBanner(brand.ShipBanner))
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "shiprig",
		Short:         "Uniform changeset → version → publish, across every ecosystem",
		Long:          "shiprig manages the whole release: it captures changesets, versions packages\nwith the shared engine (the same one changerig uses), and publishes via the\nnative package managers. One front door for .NET, Node, Go, and Rust.",
		SilenceUsage:  true,
		SilenceErrors: false,
	}

	add := commands.NewAddCmd()
	// Bare `shiprig` shows the pending release plan — the release front door's
	// natural "what would I ship?" landing. `add` (changerig's default) stays a
	// subcommand. status orients in every source mode and, in an uninitialized
	// repo, offers source-aware setup rather than erroring.
	status := commands.NewStatusCmd()
	root.RunE = status.RunE
	root.Args = status.Args
	root.Flags().AddFlagSet(status.Flags())

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
