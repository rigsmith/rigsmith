package commands

import (
	"fmt"

	"github.com/spf13/cobra"
)

// NewBranchCmd builds the `branch` command group. It complements `worktree`:
// where worktree prune reaps stale *checkouts*, branch prune reaps the local
// branch refs left behind after their worktrees (and remote branches) are gone.
func NewBranchCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "branch",
		Aliases: []string{"br"},
		Short:   "Manage local branches (prune merged/gone ones)",
	}
	cmd.AddCommand(newBranchPruneCmd())
	return cmd
}

// newBranchPruneCmd removes local branches that are done with: merged into the
// base, or — with --gone — whose upstream the remote has deleted. The current
// branch, the base, and any branch checked out in a worktree are never touched,
// and a deleted branch is recoverable from the reflog for a while, so the cost
// of a false positive is low. It mirrors `worktree prune`: merged branches go by
// default; the less-certain gone-but-unprovably-merged case is opt-in.
func newBranchPruneCmd() *cobra.Command {
	var dryRun, gone bool
	var base string
	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Delete local branches that are merged (and, with --gone, whose upstream was deleted)",
		Long: `Delete local branches that are done with. A branch is removed when it is:

  • merged  its changes are contained in the base branch
            (detects squash-merges as well as ordinary merges); or
  • gone    (with --gone) its upstream branch was deleted on the remote —
            the practical sign a PR merged once the mainline has moved on
            far enough that a merge can no longer be proven locally.

The current branch, the base branch, and any branch checked out in a worktree
are always skipped (clean those with ` + "`worktree prune`" + `). Merge state is
tested against the local base branch, so keep it current (e.g. pull main).
Deleted branches keep no worktree but are recoverable from the reflog.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			repo, _, err := openRepo(ctx)
			if err != nil {
				return err
			}
			if base == "" {
				base = repo.DefaultBranch(ctx)
			}
			branches, err := repo.LocalBranches(ctx)
			if err != nil {
				return err
			}
			// Branches checked out in a worktree can't be deleted (git refuses) and
			// belong to `worktree prune` anyway — collect them to skip cleanly.
			inWorktree := map[string]bool{}
			if wts, err := repo.WorktreeList(ctx); err == nil {
				for _, wt := range wts {
					if wt.Branch != "" {
						inWorktree[wt.Branch] = true
					}
				}
			}

			out := cmd.OutOrStdout()
			var removed, kept int
			skip := func(label, reason string) {
				kept++
				fmt.Fprintf(out, "%s %s  %s\n", DimStyle.Render("•"), HeaderStyle.Render(label), DimStyle.Render(reason))
			}
			for _, b := range branches {
				if b.Current || b.Name == base {
					continue
				}
				if inWorktree[b.Name] {
					skip(b.Name, "checked out in a worktree — use `worktree prune`")
					continue
				}
				merged, err := repo.IsMerged(ctx, b.Name, base)
				if err != nil {
					skip(b.Name, "couldn't check merge state — skipped")
					continue
				}
				reason := ""
				switch {
				case merged:
					reason = "merged into " + base
				case b.Gone && gone:
					reason = "upstream gone"
				case b.Gone:
					skip(b.Name, "upstream gone but not provably merged — skipped (use --gone)")
					continue
				default:
					skip(b.Name, fmt.Sprintf("not merged into %s — skipped", base))
					continue
				}
				if dryRun {
					removed++
					fmt.Fprintf(out, "%s %s  %s\n", WarnStyle.Render("⤳"), HeaderStyle.Render(b.Name), DimStyle.Render("would delete ("+reason+")"))
					continue
				}
				// Force-delete: we've established the branch is done with, but a
				// squash-merge or gone upstream isn't something `branch -d` recognises.
				if err := repo.DeleteBranch(ctx, b.Name, true); err != nil {
					skip(b.Name, "delete failed: "+err.Error())
					continue
				}
				removed++
				fmt.Fprintf(out, "%s %s  %s\n", OkStyle.Render("✓"), HeaderStyle.Render(b.Name), DimStyle.Render("deleted ("+reason+")"))
			}

			verb := "deleted"
			if dryRun {
				verb = "to delete"
			}
			fmt.Fprintf(out, "%s\n", DimStyle.Render(fmt.Sprintf("%d %s, %d kept", removed, verb, kept)))
			return nil
		},
	}
	cmd.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "show what would be deleted without deleting anything")
	cmd.Flags().BoolVar(&gone, "gone", false, "also delete branches whose upstream was deleted on the remote, even if a merge can't be proven")
	cmd.Flags().StringVar(&base, "base", "", "branch to test merges against (default: repo's mainline)")
	return cmd
}
