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
// version is stamped at release time via
// -ldflags "-X github.com/rigsmith/rigsmith/internal/shiprig/cli.version=...".
// It lives here rather than in cmd/shiprig because this is where fang is
// called, the same way rig keeps its own. Without it a released binary falls
// through to fang's source-build description and reports itself as unversioned.
var version = "dev"

func Execute(ctx context.Context) error {
	root := newRootCmd()
	// Bare, interactive `shiprig` (no verb/flag) lands on the menu. Routing
	// through the registered `ui` subcommand — which already carries the release
	// menu items — keeps the menu title resolving to "shiprig" (via
	// cmd.Root().Name()) and preserves cobra's unknown-command errors.
	if len(os.Args) == 1 && commands.Interactive() {
		root.SetArgs([]string{"ui"})
	}
	return fang.Execute(ctx, root, fang.WithVersion(version), fang.WithColorSchemeFunc(brand.ColorSchemeFunc(brand.AccentShip)), fang.WithBanner(brand.ShipBanner))
}

// NewRootCmd returns shiprig's full command tree without the bare-TTY menu
// routing, for consistency checks (core/cliguard) and tests.
func NewRootCmd() *cobra.Command { return newRootCmd() }

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "shiprig",
		Short:         "Uniform changeset → version → publish, across every ecosystem",
		Long:          "shipRig manages the whole release: it captures changesets, versions packages\nwith the shared engine (the same one changeRig uses), and publishes via the\nnative package managers. One front door for .NET, Node, Go, and Rust.",
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
	// BareRunE softens only the no-verb, no-flags case in an unconfigured
	// directory: orientation and exit 0, matching `rig` and `clauderig`.
	// `shiprig status` invoked by name — what a CI gate should call — is
	// unchanged and still exits non-zero.
	root.RunE = commands.BareRunE(status.RunE)
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
		newPackagesCmd(),
		newPublishCmd(),
		newTagCmd(),
		newReleaseCmd(),
		newDoctorCmd(),
	)
	return root
}

// releaseMenuItems are shiprig's own verbs, contributed to the shared changeset
// menu so the release tool's menu reflects its full surface — not just the
// lifecycle it inherits from changerig. They sit after Version (the natural
// release order: version → publish → tag → run the pipeline).
func releaseMenuItems() []commands.MenuItem {
	return []commands.MenuItem{
		{Label: "Packages", Desc: "show packages to build; include/exclude them", Build: newPackagesCmd},
		{Label: "Publish", Desc: "publish built packages to their registries", Build: newPublishCmd},
		{Label: "Tag", Desc: "create + push git tags for released versions", Build: newTagCmd},
		{Label: "Release", Desc: "run the full release pipeline", Build: newReleaseCmd},
		{Label: "Doctor", Desc: "check the release setup", Build: newDoctorCmd},
	}
}
