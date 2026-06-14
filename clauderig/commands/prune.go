package commands

import (
	"bytes"
	"fmt"
	"io"
	"strings"

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
// Unlike those targeted commands, prune deletes branches, so it ALWAYS requires
// an interactive confirmation: it previews the plan, then asks at a real
// terminal before touching anything. There is deliberately no flag to skip the
// prompt — in a non-interactive context (a script, hook, or piped run) prune
// fails outright rather than delete unattended. Use -n to preview anywhere, or
// the unattended `worktree prune` / `branch prune` for automation.
func NewPruneCmd() *cobra.Command {
	var dryRun, gone bool
	var base string
	cmd := &cobra.Command{
		Use:     "prune",
		Aliases: []string{"tidy"},
		Short:   "Remove merged/done worktrees and their branches in one sweep (interactive)",
		Long: `Tidy up after merged PRs: remove finished worktrees and branches together.

Two phases run in order (worktrees first, since a checked-out branch can't be
deleted):

  1. worktrees — remove each clean worktree whose branch is merged into base
                 (or, with --gone, whose upstream the remote deleted)
  2. branches  — delete each branch that is merged (or, with --gone, gone),
                 including the ones whose worktree phase 1 just removed

The current branch/worktree, the base, and dirty or unmerged checkouts are never
touched. Because prune deletes branches it ALWAYS asks for confirmation at a
terminal — there is no flag to skip the prompt, and a non-interactive run fails
instead of deleting unattended. Use -n to preview (works anywhere), or the
unattended worktree prune / branch prune for automation. Deleted branches are
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

			// Destructive path: never proceed without a human at the keyboard.
			// Fail before doing any work in a non-interactive context.
			if !interactive() {
				return fmt.Errorf("prune deletes branches and must be confirmed at a terminal — it won't run non-interactively; use -n to preview, or worktree prune / branch prune for unattended cleanup")
			}

			// Capture the plan into a buffer and show it *inside* the confirm dialog,
			// as a Note above the Confirm in one huh form. The whole thing is then a
			// single bubbletea view that owns and clears its own lines. (Printing the
			// plan first and then launching a separate form makes the form repaint
			// over the already-printed lines, leaving stale tails bleeding through
			// from longer scrollback — the misdisplay this avoids.)
			var plan bytes.Buffer
			planW, planB, err := run(&plan, true)
			if err != nil {
				return err
			}
			if planW+planB == 0 {
				fmt.Fprintf(out, "%s\n", DimStyle.Render("nothing to prune"))
				return nil
			}
			summary := fmt.Sprintf("%d worktree(s), %d branch(es) to remove", planW, planB)
			planText := strings.TrimRight(plan.String(), "\n") + "\n\n" + DimStyle.Render(summary)

			proceed := false
			if err := huh.NewForm(huh.NewGroup(
				huh.NewNote().Title("Prune plan").Description(planText),
				huh.NewConfirm().
					Title("Remove these?").
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
		},
	}
	cmd.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "show what would be removed without removing anything")
	cmd.Flags().BoolVar(&gone, "gone", false, "also act on items whose upstream was deleted on the remote, even if a merge can't be proven")
	cmd.Flags().StringVar(&base, "base", "", "branch to test merges against (default: repo's mainline)")
	return cmd
}
