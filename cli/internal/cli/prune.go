package cli

import (
	"github.com/spf13/cobra"
)

// newPruneCmd surfaces clauderig's top-level prune under `rig`: one sweep that
// removes merged/done worktrees and then their branches (and any other merged or,
// with --gone, gone branches). Like the worktree/branch aliases it is a thin
// passthrough — all arguments, flags, and the exit status belong to
// `clauderig prune`; the implementation stays in clauderig.
func newPruneCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "prune [args...]",
		Short: "Tidy merged worktrees + branches — delegates to `clauderig prune`",
		Long: "rig prune forwards to `clauderig prune` (alias `tidy`): remove merged/done\n" +
			"worktrees first, then their branches and any other merged (or, with --gone,\n" +
			"gone-upstream) branches. It previews and asks before acting (-y skips, -n\n" +
			"previews). All arguments and flags pass through unchanged — e.g. `rig prune -n`,\n" +
			"`rig prune --gone -y`, `rig tidy --help`.",
		Aliases: []string{"tidy"},
		// Forward flags like -n/--gone/--base straight to clauderig rather than
		// letting cobra parse them against rig.
		DisableFlagParsing: true,
		RunE:               forwardToClauderig("prune"),
	}
}
