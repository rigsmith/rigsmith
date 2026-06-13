package cli

import (
	"github.com/spf13/cobra"
)

// newBranchCmd surfaces clauderig's branch command group under `rig`, the
// companion to `rig worktree`: where worktree prune reaps stale checkouts,
// `rig branch prune` reaps the local branch refs left behind. Like the worktree
// alias it is a thin passthrough — all arguments, flags, and the exit status
// belong to `clauderig branch`; the implementation stays in clauderig.
func newBranchCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "branch [args...]",
		Short: "Manage local branches — delegates to `clauderig branch`",
		Long: "rig branch forwards to `clauderig branch`, which prunes local branches that\n" +
			"are merged (or, with --gone, whose upstream the remote deleted). All arguments\n" +
			"and flags pass through unchanged — e.g. `rig br prune -n`, `rig branch prune\n" +
			"--gone`, `rig branch --help`.",
		Aliases: []string{"br"},
		// Forward flags like -n/--gone/--base straight to clauderig rather than
		// letting cobra parse them against rig.
		DisableFlagParsing: true,
		RunE:               forwardToClauderig("branch"),
	}
}
