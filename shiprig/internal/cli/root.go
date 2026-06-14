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
	// Bare, interactive `shiprig` opens the menu — a discoverable landing with the
	// next step pre-selected. With args/flags, or off a TTY, it stays the release
	// front door's `status` ("what would I ship?"), the answer CI and pipes rely
	// on. status orients in every source mode and offers source-aware setup in an
	// uninitialized repo rather than erroring.
	status := commands.NewStatusCmd()
	root.RunE = bareMenuOr(status.RunE)
	root.Args = status.Args
	root.Flags().AddFlagSet(status.Flags())

	root.AddCommand(
		commands.NewInitCmd(),
		add,
		commands.NewStatusCmd(),
		commands.NewVersionCmd(),
		commands.NewInfoCmd(),
		commands.NewConfigCmd(),
		commands.NewUICmd(releaseMenuItems()...),
		commands.NewPreCmd(),
		newPublishCmd(),
		newTagCmd(),
		newReleaseCmd(),
	)
	return root
}

// releaseMenuItems are shiprig's own verbs, contributed to the shared changeset
// menu so the release tool's menu reflects its full surface — not just the
// lifecycle it inherits from changerig. They sit after Version (the natural
// release order: version → publish → tag → run the pipeline).
func releaseMenuItems() []commands.MenuItem {
	return []commands.MenuItem{
		{Label: "Publish", Desc: "publish built packages to their registries", Build: newPublishCmd},
		{Label: "Tag", Desc: "create + push git tags for released versions", Build: newTagCmd},
		{Label: "Release", Desc: "run the full release pipeline", Build: newReleaseCmd},
	}
}

// bareMenuOr returns a RunE that opens the interactive menu when shiprig is
// invoked truly bare (no args, no flags) on a TTY, and otherwise falls through
// to fallback (status — the CI/pipe path). Gating on "truly bare" keeps every
// flag-driven and scripted invocation on the deterministic non-interactive path.
func bareMenuOr(fallback func(*cobra.Command, []string) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 && cmd.Flags().NFlag() == 0 && commands.Interactive() {
			ui := commands.NewUICmd(releaseMenuItems()...)
			ui.SetContext(cmd.Context())
			ui.SetOut(cmd.OutOrStdout())
			ui.SetErr(cmd.ErrOrStderr())
			return ui.RunE(ui, nil)
		}
		return fallback(cmd, args)
	}
}
