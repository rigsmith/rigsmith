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
)

// version is stamped at release time via -ldflags "-X main.version=...". Without
// it a released binary falls through to fang's source-build description, so
// every published changerig used to introduce itself as a build from someone
// else's machine.
var version = "dev"

func main() {
	if err := run(context.Background()); err != nil {
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	root := commands.NewRootCmd()

	// Bare, interactive `changerig` (no verb/flag) lands on the menu. Routing
	// through the registered `ui` subcommand — rather than running a standalone
	// NewUICmd — keeps the menu title resolving to "changerig" (via
	// cmd.Root().Name()) and preserves cobra's unknown-command errors.
	if len(os.Args) == 1 && commands.Interactive() {
		root.SetArgs([]string{"ui"})
	}
	return fang.Execute(ctx, root, fang.WithVersion(version), fang.WithColorSchemeFunc(brand.ColorSchemeFunc(brand.AccentChange)), fang.WithBanner(brand.ChangeBanner))
}
