package cli

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"
)

// newWorktreeCmd surfaces clauderig's worktree command group under `rig` as a
// convenience alias, so the disciplined worktree workflow (sibling checkouts
// each opened in their own VS Code window) is reachable from the same CLI as the
// rest of the dev loop. It is a thin passthrough: every argument and flag, and
// the exit status, belong to `clauderig worktree`. The authority and
// implementation stay in clauderig — rig only forwards.
func newWorktreeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "worktree [args...]",
		Short: "Manage git worktrees — delegates to `clauderig worktree`",
		Long: "rig worktree forwards to `clauderig worktree`, where the disciplined worktree\n" +
			"workflow lives: sibling checkouts at <repo>-worktrees/<branch>, each opened in\n" +
			"its own VS Code window. All arguments and flags pass through unchanged — e.g.\n" +
			"`rig wt new feat/x`, `rig wt prune -n`, `rig wt --help`.",
		Aliases: []string{"wt"},
		// Forward every token (including flags like -n/--base, which would
		// otherwise collide with rig's own --dry-run or error as unknown) straight
		// to clauderig instead of letting cobra parse them against rig.
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			bin, err := clauderigBin()
			if err != nil {
				return err
			}
			c := exec.CommandContext(cmd.Context(), bin, append([]string{"worktree"}, args...)...)
			c.Stdin = os.Stdin
			c.Stdout = cmd.OutOrStdout()
			c.Stderr = cmd.ErrOrStderr()
			return c.Run()
		},
	}
}

// clauderigBin locates the clauderig binary that backs `rig worktree`. rig and
// clauderig ship together, so it's normally `clauderig` on PATH; the -dev
// fallback keeps the alias working inside this repo's own dev launchers.
func clauderigBin() (string, error) {
	for _, name := range []string{"clauderig", "clauderig-dev"} {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("clauderig not found on PATH — `rig worktree` delegates to it; install clauderig (it ships alongside rig)")
}
