// Command changerig is the lean changeset tool: the changeset lifecycle
// (init → add → status → version) isolated from the heavier release
// orchestration that shiprig layers on top. Both share the same engine
// (rigsmith/core) and the same command builders (changerig/commands), so a
// changeset and a version run mean exactly the same thing in either tool.
//
// `changeset` is accepted as an alias for muscle memory from the JS @changesets
// and the original net-changesets tool.
package main

import (
	"context"
	"os"

	"github.com/rigsmith/rigsmith/core/brand"
	"github.com/rigsmith/rigsmith/core/fang"
	"github.com/rigsmith/rigsmith/internal/changerig/commands"
	"github.com/spf13/cobra"
)

func main() {
	if err := run(context.Background()); err != nil {
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	root := &cobra.Command{
		Use:           "changerig",
		Aliases:       []string{"changeset"},
		Short:         "Changesets: capture intent, then version across every ecosystem",
		Long:          "changerig manages changeset files and turns them into version bumps and\nchangelogs. One engine decides bumps, cascade, and changelog for .NET, Node,\nGo, and Rust alike.",
		SilenceUsage:  true,
		SilenceErrors: false,
	}

	add := commands.NewAddCmd()
	// With args/flags (e.g. `changerig -m "…"`), or off a TTY, bare `changerig`
	// behaves like `changerig add` — scripted/flag-driven use is unchanged.
	root.RunE = add.RunE
	root.Args = add.Args
	root.Flags().AddFlagSet(add.Flags())

	root.AddCommand(
		commands.NewInitCmd(),
		add,
		commands.NewStatusCmd(),
		commands.NewBrowseCmd(),
		commands.NewVersionCmd(),
		commands.NewPreCmd(),
		commands.NewInfoCmd(),
		commands.NewConfigCmd(),
		commands.NewChangelogCmd(),
		commands.NewUICmd(),
	)

	// Bare, interactive `changerig` (no verb/flag) lands on the menu. Routing
	// through the registered `ui` subcommand — rather than running a standalone
	// NewUICmd — keeps the menu title resolving to "changerig" (via
	// cmd.Root().Name()) and preserves cobra's unknown-command errors.
	if len(os.Args) == 1 && commands.Interactive() {
		root.SetArgs([]string{"ui"})
	}
	return fang.Execute(ctx, root, fang.WithColorSchemeFunc(brand.ColorSchemeFunc(brand.AccentChange)), fang.WithBanner(brand.ChangeBanner))
}
