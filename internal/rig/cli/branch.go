package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/rigsmith/rigsmith/core/climenu"
	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/spf13/cobra"
)

// newBranchCmd builds the `branch` command group. It complements `worktree`:
// where worktree prune reaps stale *checkouts*, branch prune reaps the local
// branch refs left behind after their worktrees (and remote branches) are gone.
// list/rm/prune mirror the worktree group's management trio.
func newBranchCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "branch",
		Aliases: []string{"br"},
		Short:   "List/remove/prune local branches",
		// Bare `rig branch` on a TTY opens the subcommand menu; with a verb or off a
		// TTY the subcommands stand (and `branch -h` still prints help). `rm <branch>`
		// stays command-line.
		RunE: func(cmd *cobra.Command, args []string) error {
			if stdinStdoutTTY() {
				return climenu.Run(cmd)
			}
			return cmd.Help()
		},
	}
	cmd.AddCommand(newBranchListCmd(), newBranchRemoveCmd(), newBranchPruneCmd())
	return cmd
}

// branchStatus is a one-word classification of a local branch, shared by list
// (to annotate) and conceptually echoed by prune (which acts on it).
type branchStatus struct {
	tag    string                 // current | worktree | merged | gone | unmerged
	render func(...string) string // lipgloss style for the tag
}

// classifyBranch labels b against base and the set of worktree-attached
// branches. merge state is only computed when needed (it shells out per branch).
func classifyBranch(ctx context.Context, repo *gitrepo.Repo, b gitrepo.Branch, base string, inWorktree map[string]bool) branchStatus {
	switch {
	case b.Current:
		return branchStatus{"current", OkStyle.Render}
	case inWorktree[b.Name]:
		return branchStatus{"worktree", HeaderStyle.Render}
	}
	if merged, err := repo.IsMerged(ctx, b.Name, base); err == nil && merged {
		return branchStatus{"merged", OkStyle.Render}
	}
	if b.Gone {
		return branchStatus{"gone", WarnStyle.Render}
	}
	return branchStatus{"unmerged", DimStyle.Render}
}

// worktreeBranches is the set of branches checked out in a worktree — they can't
// be deleted (git refuses) and belong to the worktree commands. A list failure
// yields an empty set, so callers degrade to "none attached" rather than erroring.
func worktreeBranches(ctx context.Context, repo *gitrepo.Repo) map[string]bool {
	set := map[string]bool{}
	if wts, err := repo.WorktreeList(ctx); err == nil {
		for _, wt := range wts {
			if wt.Branch != "" {
				set[wt.Branch] = true
			}
		}
	}
	return set
}

// branchCompletion offers local branch names for a single positional arg.
func branchCompletion(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	repo, _, err := openRepo(cmd.Context())
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	branches, err := repo.LocalBranches(cmd.Context())
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var names []string
	for _, b := range branches {
		if !b.Current {
			names = append(names, b.Name)
		}
	}
	return names, cobra.ShellCompDirectiveNoFileComp
}

func newBranchListCmd() *cobra.Command {
	var base string
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List local branches with their prune status",
		Long: `List the repo's local branches, each tagged with how prune sees it:

  • current   the checked-out branch (never pruned)
  • worktree  checked out in a worktree (clean with ` + "`worktree prune`" + `)
  • merged    contained in the base branch (pruned by default)
  • gone      its upstream was deleted on the remote (pruned by default)
  • unmerged  not contained in base and no gone upstream (kept)`,
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
			inWorktree := worktreeBranches(ctx, repo)

			width := 0
			for _, b := range branches {
				if len(b.Name) > width {
					width = len(b.Name)
				}
			}
			out := cmd.OutOrStdout()
			for _, b := range branches {
				st := classifyBranch(ctx, repo, b, base, inWorktree)
				marker := "  "
				if b.Current {
					marker = OkStyle.Render("* ")
				}
				fmt.Fprintf(out, "%s%-*s  %s\n", marker, width, HeaderStyle.Render(b.Name), st.render(st.tag))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&base, "base", "", "branch to test merges against (default: repo's mainline)")
	return cmd
}

func newBranchRemoveCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:               "rm <branch>",
		Aliases:           []string{"remove"},
		Short:             "Remove a local branch (-f to force an unmerged one)",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: branchCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			repo, _, err := openRepo(ctx)
			if err != nil {
				return err
			}
			branch := args[0]
			if cur, err := repo.CurrentBranch(ctx); err == nil && cur == branch {
				return fmt.Errorf("can't remove the current branch %q — switch away first", branch)
			}
			if worktreeBranches(ctx, repo)[branch] {
				return fmt.Errorf("%q is checked out in a worktree — remove that with `worktree rm` first", branch)
			}
			// Without --force this is `branch -d`, which refuses an unmerged branch.
			if err := repo.DeleteBranch(ctx, branch, force); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s removed %s\n", OkStyle.Render("✓"), HeaderStyle.Render(branch))
			return nil
		},
	}
	cmd.Flags().BoolVarP(&force, "force", "f", false, "remove even if the branch isn't fully merged")
	return cmd
}

// newBranchPruneCmd removes local branches that are done with: merged into the
// base, or whose upstream the remote has deleted. The current branch, the base,
// and any branch checked out in a worktree are never touched, and a deleted
// branch is recoverable from the reflog for a while, so the cost of a false
// positive is low. It mirrors `worktree prune`: both merged and gone-upstream
// branches go by default; --keep-gone keeps the gone-but-unprovably-merged ones.
func newBranchPruneCmd() *cobra.Command {
	var dryRun, keepGone bool
	var base string
	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Delete local branches that are merged or whose upstream was deleted",
		Long: `Delete local branches that are done with. A branch is removed when it is:

  • merged  its changes are contained in the base branch
            (detects squash-merges as well as ordinary merges); or
  • gone    its upstream branch was deleted on the remote — the practical sign
            a PR merged once the mainline has moved on far enough that a merge
            can no longer be proven locally. Pass --keep-gone to keep these.

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
			out := cmd.OutOrStdout()
			removed, kept, err := pruneBranches(ctx, out, repo, base, dryRun, !keepGone, nil)
			if err != nil {
				return err
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
	cmd.Flags().BoolVar(&keepGone, "keep-gone", false, keepGoneUsage)
	cmd.Flags().StringVar(&base, "base", "", "branch to test merges against (default: repo's mainline)")
	return cmd
}

// pruneBranches deletes local branches that are merged into base or (with gone)
// whose upstream the remote deleted. It prints one line per branch and returns
// the removed/kept counts; the caller prints the summary. The current branch,
// the base, and branches still checked out in a worktree are skipped — except
// any in detached, whose worktrees a combined sweep just removed (or, in
// dry-run, would remove), so they're now deletable.
func pruneBranches(ctx context.Context, out io.Writer, repo *gitrepo.Repo, base string, dryRun, gone bool, detached map[string]bool) (removed, kept int, err error) {
	branches, err := repo.LocalBranches(ctx)
	if err != nil {
		return 0, 0, err
	}
	// Branches checked out in a worktree can't be deleted (git refuses); skip
	// them unless their worktree was just freed by a combined sweep.
	inWorktree := worktreeBranches(ctx, repo)
	for name := range detached {
		delete(inWorktree, name)
	}

	var rows []pruneRow
	skip := func(label, reason string) {
		kept++
		rows = append(rows, pruneRow{name: label, kind: pruneSkip, state: "skip", why: reason})
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
			skip(b.Name, "couldn't check merge state")
			continue
		}
		reason := ""
		switch {
		case merged:
			reason = "merged into " + base
		case b.Gone && gone:
			reason = "upstream gone"
		case b.Gone:
			skip(b.Name, "upstream gone — kept (--keep-gone)")
			continue
		default:
			skip(b.Name, "not merged into "+base)
			continue
		}
		if dryRun {
			removed++
			rows = append(rows, pruneRow{name: b.Name, kind: prunePlan, state: "will delete", why: reason})
			continue
		}
		// Force-delete: we've established the branch is done with, but a
		// squash-merge or gone upstream isn't something `branch -d` recognises.
		if err := repo.DeleteBranch(ctx, b.Name, true); err != nil {
			skip(b.Name, "delete failed: "+err.Error())
			continue
		}
		removed++
		rows = append(rows, pruneRow{name: b.Name, kind: pruneDone, state: "deleted", why: reason})
	}
	renderPruneTable(out, rows)
	return removed, kept, nil
}
