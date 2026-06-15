// Package cli defines shiprig's command surface. shiprig is the release front
// door: it re-exposes the full changeset lifecycle (init/add/status/version/info)
// from the shared changerig/commands package and adds the release-orchestration
// verbs (publish, tag, pre) on top.
package cli

import (
	"context"
	"os"

	"github.com/rigsmith/rigsmith/core/brand"
	"github.com/rigsmith/rigsmith/core/fang"
	"github.com/rigsmith/rigsmith/internal/changerig/commands"
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
	root.PersistentFlags().BoolVar(&noEnv, "no-env", false, "skip .env/.env.local loading for this run")

	add := commands.NewAddCmd()
	// With args/flags, or off a TTY, bare `shiprig` stays the release front door's
	// `status` ("what would I ship?") — the answer CI and pipes rely on. status
	// orients in every source mode and offers source-aware setup in an
	// uninitialized repo rather than erroring.
	status := commands.NewStatusCmd()
	root.RunE = status.RunE
	root.Args = status.Args
	root.Flags().AddFlagSet(status.Flags())

	root.AddCommand(
		newInitCmd(),
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
		newDoctorCmd(),
	)

	// Bare, interactive `shiprig` (no verb/flag) lands on the menu. Routing
	// through the registered `ui` subcommand — which already carries the release
	// menu items — keeps the menu title resolving to "shiprig" (via
	// cmd.Root().Name()) and preserves cobra's unknown-command errors.
	if len(os.Args) == 1 && commands.Interactive() {
		root.SetArgs([]string{"ui"})
	}
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
		{Label: "Doctor", Desc: "check the release setup", Build: newDoctorCmd},
	}
}
