package commands

import (
	"fmt"

	"github.com/spf13/cobra"
)

// NewPruneCmd builds the top-level `prune` (alias `tidy`): one sweep that clears
// both halves of a finished branch. It runs the worktree sweep first — git won't
// delete a branch that's checked out — then the branch sweep, which picks up the
// branches just freed along with any others that are merged (or, with --gone,
// whose upstream the remote deleted). For a worktree+branch pair that landed
// together, this is the single "tidy up after a merged PR" command;
// `worktree prune` and `branch prune` remain for one side at a time.
func NewPruneCmd() *cobra.Command {
	var dryRun, gone bool
	var base string
	cmd := &cobra.Command{
		Use:     "prune",
		Aliases: []string{"tidy"},
		Short:   "Remove merged/done worktrees and their branches in one sweep",
		Long: `Tidy up after merged PRs: remove finished worktrees and branches together.

Two phases run in order (worktrees first, since a checked-out branch can't be
deleted):

  1. worktrees — remove each clean worktree whose branch is merged into base
                 (or, with --gone, whose upstream the remote deleted)
  2. branches  — delete each branch that is merged (or, with --gone, gone),
                 including the ones whose worktree phase 1 just removed

The current branch/worktree, the base, and dirty or unmerged checkouts are never
touched. Deleted branches are recoverable from the reflog. Merge state is tested
against the local base branch, so keep it current (e.g. pull main).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			repo, root, err := openRepo(ctx)
			if err != nil {
				return err
			}
			if base == "" {
				base = repo.DefaultBranch(ctx)
			}
			out := cmd.OutOrStdout()

			// Phase 1: worktrees. deleteBranches stays off — phase 2 owns branch
			// deletion (and force-deletes squash-merges, which `branch -d` won't).
			fmt.Fprintln(out, HeaderStyle.Render("worktrees"))
			wRemoved, _, freed, err := pruneWorktrees(ctx, out, repo, root, base, dryRun, gone, false)
			if err != nil {
				return err
			}

			// Phase 2: branches. The freed set tells the branch sweep to stop
			// treating the just-removed worktrees' branches as still attached.
			detached := map[string]bool{}
			for _, b := range freed {
				detached[b] = true
			}
			fmt.Fprintln(out, HeaderStyle.Render("branches"))
			bRemoved, _, err := pruneBranches(ctx, out, repo, base, dryRun, gone, detached)
			if err != nil {
				return err
			}

			verb := "removed"
			if dryRun {
				verb = "to remove"
			}
			fmt.Fprintf(out, "%s\n", DimStyle.Render(fmt.Sprintf("%d worktrees, %d branches %s", wRemoved, bRemoved, verb)))
			return nil
		},
	}
	cmd.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "show what would be removed without removing anything")
	cmd.Flags().BoolVar(&gone, "gone", false, "also act on items whose upstream was deleted on the remote, even if a merge can't be proven")
	cmd.Flags().StringVar(&base, "base", "", "branch to test merges against (default: repo's mainline)")
	return cmd
}
