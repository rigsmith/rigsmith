package cli

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"
)

// newWorktreeCmd makes worktrees first-class in rig: they're the parallel-dev
// loop's unit of work, and the -dev/-wt launchers and the pinned build route are
// rig's domain (the build loop), not Claude sync. It's a thin passthrough — every
// argument, flag, and the exit status belong to `clauderig worktree`, where the
// mechanics live — but framed and surfaced (help + menu) as part of rig's loop.
func newWorktreeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "worktree [args...]",
		Short: "Parallel-dev worktrees — and pin which one the -dev tools build from",
		Long: "rig worktree (alias `rig wt`) drives the parallel-dev loop: sibling checkouts\n" +
			"at <repo>-worktrees/<branch> that you build and run independently with the\n" +
			"-dev/-wt launchers, plus an active route you pin so a bare `rig-dev` builds\n" +
			"from a chosen tree.\n\n" +
			"Common flows:\n" +
			"  rig wt new feat/x     create a worktree (+ branch)\n" +
			"  rig wt use [query]    pin a worktree as the -dev route\n" +
			"  rig wt active         show the pinned route\n" +
			"  rig wt unset          clear the pin\n" +
			"  rig wt list | prune   list / sweep clean, merged worktrees\n\n" +
			"The worktree mechanics live in `clauderig worktree`; rig forwards to it so\n" +
			"the whole build loop is reachable from one CLI. All args and flags pass\n" +
			"through unchanged (e.g. `rig wt prune -n`).",
		Aliases: []string{"wt"},
		// Forward every token (including flags like -n/--base, which would
		// otherwise collide with rig's own --dry-run or error as unknown) straight
		// to clauderig instead of letting cobra parse them against rig.
		DisableFlagParsing: true,
		RunE:               forwardToClauderig("worktree"),
	}
}

// worktreeForward builds a menu command that runs `clauderig worktree <sub>`
// through the same passthrough that backs `rig worktree`, so the menu's worktree
// actions share rig's forwarder (and its clauderig-not-found message).
func worktreeForward(sub string) *cobra.Command {
	return &cobra.Command{
		Use: sub,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return forwardToClauderig("worktree")(cmd, []string{sub})
		},
	}
}

// forwardToClauderig builds a RunE that execs `clauderig <group> <args...>`,
// passing stdio and the exit status straight through. It backs the thin rig
// aliases (worktree, branch) whose authority lives in clauderig.
func forwardToClauderig(group string) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		bin, err := clauderigBin()
		if err != nil {
			return err
		}
		c := exec.CommandContext(cmd.Context(), bin, append([]string{group}, args...)...)
		c.Stdin = os.Stdin
		c.Stdout = cmd.OutOrStdout()
		c.Stderr = cmd.ErrOrStderr()
		return c.Run()
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
