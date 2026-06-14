// Package cli defines shiprig's command surface. shiprig is the release front
// door: it re-exposes the full changeset lifecycle (init/add/status/version/info)
// from the shared changerig/commands package and adds the release-orchestration
// verbs (publish, tag, pre) on top.
package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rigsmith/changerig/commands"
	"github.com/rigsmith/core/brand"
	"github.com/rigsmith/core/fang"
	"github.com/spf13/cobra"
)

// Execute builds the command tree and runs it through fang.
func Execute(ctx context.Context) error {
	warnIfDeprecatedAlias()
	return fang.Execute(ctx, newRootCmd(), fang.WithColorSchemeFunc(brand.ColorSchemeFunc(brand.AccentShip)), fang.WithBanner(brand.ShipBanner))
}

// warnIfDeprecatedAlias prints a one-line notice when the binary is invoked
// under its old name. shiprig was renamed from relrig; the relrig name keeps
// working as a deprecated alias (a symlink/copy installed alongside, or a
// renamed download) so muscle memory and existing CI don't break.
func warnIfDeprecatedAlias() {
	base := strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe")
	if base == "relrig" {
		fmt.Fprintln(os.Stderr, "note: 'relrig' was renamed to 'shiprig'; 'relrig' is now a deprecated alias.")
	}
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
	// Bare `shiprig` behaves like `shiprig add`.
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
