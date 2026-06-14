package commands

import (
	"bytes"
	"fmt"
	"io"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
)

// NewPruneCmd builds the top-level `prune` (alias `tidy`): one sweep that clears
// both halves of a finished branch. It runs the worktree sweep first — git won't
// delete a branch that's checked out — then the branch sweep, which picks up the
// branches just freed along with any others that are merged (or, with --gone,
// whose upstream the remote deleted). For a worktree+branch pair that landed
// together, this is the single "tidy up after a merged PR" command;
// `worktree prune` and `branch prune` remain for one side at a time.
//
// Unlike those targeted commands, prune also deletes branches by default, so it
// previews the plan and asks before acting. --yes skips the prompt; -n previews
// only; a non-interactive run refuses without --yes rather than delete silently.
func NewPruneCmd() *cobra.Command {
	var dryRun, gone, yes bool
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
touched. prune previews the plan and asks before acting (--yes skips the prompt,
-n previews only); a non-interactive run needs --yes. Deleted branches are
recoverable from the reflog. Merge state is tested against the local base
branch, so keep it current (e.g. pull main).`,
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

			// run sweeps both phases (worktrees, then branches) to w, returning the
			// removed counts. The freed set carries phase 1's branches into phase 2
			// so they're no longer treated as worktree-attached.
			run := func(w io.Writer, dry bool) (worktrees, branches int, err error) {
				fmt.Fprintln(w, HeaderStyle.Render("worktrees"))
				wRemoved, _, freed, err := pruneWorktrees(ctx, w, repo, root, base, dry, gone, false)
				if err != nil {
					return 0, 0, err
				}
				detached := map[string]bool{}
				for _, b := range freed {
					detached[b] = true
				}
				fmt.Fprintln(w, HeaderStyle.Render("branches"))
				bRemoved, _, err := pruneBranches(ctx, w, repo, base, dry, gone, detached)
				return wRemoved, bRemoved, err
			}

			if dryRun {
				w, b, err := run(out, true)
				if err != nil {
					return err
				}
				fmt.Fprintf(out, "%s\n", DimStyle.Render(fmt.Sprintf("%d worktrees, %d branches to remove", w, b)))
				return nil
			}

			if !yes {
				// Preview the plan, then confirm before touching anything.
				planW, planB, err := run(out, true)
				if err != nil {
					return err
				}
				if planW+planB == 0 {
					fmt.Fprintf(out, "%s\n", DimStyle.Render("nothing to prune"))
					return nil
				}
				fmt.Fprintf(out, "%s\n", DimStyle.Render(fmt.Sprintf("%d worktrees, %d branches to remove", planW, planB)))
				if !interactive() {
					return fmt.Errorf("refusing to prune without confirmation — re-run with --yes (or -n to preview)")
				}
				proceed := false
				if err := huh.NewForm(huh.NewGroup(
					huh.NewConfirm().
						Title(fmt.Sprintf("Remove these %d worktree(s) and %d branch(es)?", planW, planB)).
						Affirmative("Yes, prune").
						Negative("Cancel").
						Value(&proceed),
				)).Run(); err != nil || !proceed {
					fmt.Fprintf(out, "%s\n", DimStyle.Render("aborted"))
					return nil
				}

				// Act for real into a buffer; on a clean run (results match the plan
				// the user just approved) print a terse summary, otherwise flush the
				// detail so any failure or drift is visible.
				var real bytes.Buffer
				w, b, err := run(&real, false)
				if err != nil {
					return err
				}
				if w != planW || b != planB {
					io.Copy(out, &real)
				}
				fmt.Fprintf(out, "%s\n", OkStyle.Render(fmt.Sprintf("✓ %d worktrees, %d branches removed", w, b)))
				return nil
			}

			w, b, err := run(out, false)
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "%s\n", DimStyle.Render(fmt.Sprintf("%d worktrees, %d branches removed", w, b)))
			return nil
		},
	}
	cmd.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "show what would be removed without removing anything")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	cmd.Flags().BoolVar(&gone, "gone", false, "also act on items whose upstream was deleted on the remote, even if a merge can't be proven")
	cmd.Flags().StringVar(&base, "base", "", "branch to test merges against (default: repo's mainline)")
	return cmd
}
