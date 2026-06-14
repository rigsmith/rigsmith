// Command clauderig syncs your Claude Code environment (config, skills, and
// session history) across machines via your own git remote, correcting paths
// across OSes on restore. The fourth rig: a single statically-linked Go binary,
// zero runtime deps, installable by curl|sh / Homebrew / Scoop on any machine —
// the same north-star as rig / shiprig / changerig.
//
// The two hard problems the community tools punt on — cross-OS path correction
// and not leaking secrets — are clauderig's reason to exist. See
// docs/CLAUDERIG-DESIGN.md for the full spec.
package main

import (
	"context"
	"os"

	"github.com/rigsmith/clauderig/commands"
	"github.com/rigsmith/core/brand"
	"github.com/rigsmith/core/fang"
	"github.com/spf13/cobra"
)

// version is stamped at release time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := run(context.Background()); err != nil {
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	root := &cobra.Command{
		Use:     "clauderig",
		Version: version,
		Short:   "Sync your Claude Code setup across machines, path-correct on restore",
		Long: "clauderig syncs your Claude Code config, skills, and session history to your\n" +
			"own git remote and restores it on any machine — rewriting paths across OSes\n" +
			"and never leaking secrets. Pick up where you left off on a different computer.",
		SilenceUsage:  true,
		SilenceErrors: false,
	}

	root.AddCommand(
		commands.NewInitCmd(),
		commands.NewSyncCmd(),
		commands.NewPullCmd(),
		commands.NewRestoreCmd(),
		commands.NewStatusCmd(),
		commands.NewGuardCmd(),
		commands.NewWorktreeCmd(),
		commands.NewBranchCmd(),
		commands.NewPruneCmd(),
		commands.NewGuideCmd(),
		commands.NewDoctorCmd(version),
		commands.NewConfigCmd(),
		commands.NewUICmd(),
	)
	root.AddCommand(commands.ScopeCommands()...) // global (alias: hooks) / project / local

	// Bare, interactive `clauderig` lands on the dashboard — a discoverable hub
	// with the next step in view. Off a TTY (or with any verb/flag) the normal
	// help/dispatch stands, so hooks, scripts, and `clauderig -h` are unchanged.
	// Routing through the `ui` verb (not a root RunE) keeps cobra's
	// unknown-command errors intact.
	if len(os.Args) == 1 && commands.Interactive() {
		root.SetArgs([]string{"ui"})
	}
	return fang.Execute(ctx, root, fang.WithVersion(version), fang.WithColorSchemeFunc(brand.ColorSchemeFunc(brand.AccentClaude)), fang.WithBanner(brand.ClaudeBanner))
}
