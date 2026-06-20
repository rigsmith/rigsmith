package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/spf13/cobra"
)

// pruneKind is a row's disposition, driving its glyph and color.
type pruneKind int

const (
	prunePlan pruneKind = iota // ⤳ would remove/delete (dry preview)
	pruneDone                  // ✓ removed/deleted (acted)
	pruneSkip                  // • left alone, with a reason
)

func (k pruneKind) glyph() string {
	switch k {
	case prunePlan:
		return WarnStyle.Render("⤳")
	case pruneDone:
		return OkStyle.Render("✓")
	default:
		return DimStyle.Render("•")
	}
}

func (k pruneKind) style() lipgloss.Style {
	switch k {
	case prunePlan:
		return WarnStyle
	case pruneDone:
		return OkStyle
	default:
		return DimStyle
	}
}

// pruneRow is one worktree/branch line for the aligned name | state | why table.
type pruneRow struct {
	name  string
	kind  pruneKind
	state string // "will remove", "removed", "will delete", "deleted", "skip"
	why   string // reason — "merged", "not merged into main", "uncommitted changes", …
}

// renderPruneTable prints rows as three aligned columns (name · state · why)
// under a dim header. Cells are padded as plain text before styling so the ANSI
// colors don't throw off the widths.
func renderPruneTable(out io.Writer, rows []pruneRow) {
	if len(rows) == 0 {
		fmt.Fprintln(out, "  "+DimStyle.Render("(none)"))
		return
	}
	nameW, stateW := runeLen("name"), runeLen("state")
	for _, r := range rows {
		nameW = max(nameW, runeLen(r.name))
		stateW = max(stateW, runeLen(r.state))
	}
	// Header aligns under the "  <glyph> " gutter (two spaces + glyph + space).
	fmt.Fprintf(out, "    %s  %s  %s\n",
		DimStyle.Render(padRight("name", nameW)),
		DimStyle.Render(padRight("state", stateW)),
		DimStyle.Render("why"))
	for _, r := range rows {
		fmt.Fprintf(out, "  %s %s  %s  %s\n",
			r.kind.glyph(),
			HeaderStyle.Render(padRight(r.name, nameW)),
			r.kind.style().Render(padRight(r.state, stateW)),
			DimStyle.Render(r.why))
	}
}

// pruneScope selects which halves of "done" work `prune` sweeps: both by
// default, or one side via --worktrees / --branches. It's also what the confirm
// screen's w/b/a keys toggle between.
type pruneScope int

const (
	scopeBoth pruneScope = iota
	scopeWorktrees
	scopeBranches
)

// phases maps a scope to which sweeps run.
func (s pruneScope) phases() (doWT, doBR bool) {
	switch s {
	case scopeWorktrees:
		return true, false
	case scopeBranches:
		return false, true
	default:
		return true, true
	}
}

// pruneCounts is what a sweep removed (or, in a dry run, would remove).
type pruneCounts struct{ worktrees, branches int }

func (c pruneCounts) total() int { return c.worktrees + c.branches }

// summary renders the trailing count line for a scope, e.g. "2 worktrees, 5
// branches to remove" (or "removed" once done).
func (c pruneCounts) summary(scope pruneScope, dry bool) string {
	verb := "removed"
	if dry {
		verb = "to remove"
	}
	switch scope {
	case scopeWorktrees:
		return fmt.Sprintf("%d worktrees %s", c.worktrees, verb)
	case scopeBranches:
		return fmt.Sprintf("%d branches %s", c.branches, verb)
	default:
		return fmt.Sprintf("%d worktrees, %d branches %s", c.worktrees, c.branches, verb)
	}
}

// newPruneCmd builds the top-level `prune` (alias `tidy`) — the single verb for
// clearing finished work. With neither selector it clears both halves of a
// merged branch: the worktree sweep first (git won't delete a checked-out
// branch), then the branch sweep, which picks up the just-freed branches plus
// any others merged or whose upstream the remote deleted. --worktrees / --branches
// scope it to one side (the old `worktree prune` / `branch prune`).
//
// Because prune deletes things it previews the plan and confirms by default;
// -y/--yes skips the prompt (the idiom shared with version/publish/release) and
// -n previews without removing. Without -y a non-interactive run (script, hook,
// or pipe) refuses rather than delete unattended.
func newPruneCmd() *cobra.Command {
	var dryRun, keepGone, yes, wtOnly, brOnly bool
	var base string
	cmd := &cobra.Command{
		Use:     "prune",
		Aliases: []string{"tidy"},
		Short:   "Remove merged/done worktrees and their branches in one sweep",
		Long: `Tidy up after merged PRs: remove finished worktrees and branches together.

By default two phases run in order (worktrees first, since a checked-out branch
can't be deleted):

  1. worktrees — remove each clean worktree whose branch is merged into base
                 or whose upstream the remote deleted (--keep-gone to keep those)
  2. branches  — delete each branch that is merged or whose upstream is gone,
                 including the ones whose worktree phase 1 just removed

Scope it with --worktrees or --branches to sweep one side only. A deleted
upstream is the strongest "done" signal — it also catches squash-merges the local
patch-id check can't prove — so gone-upstream items are removed by default; pass
--keep-gone to keep them. The current branch/worktree, the base, and dirty or
unmerged checkouts are never touched.

Because prune deletes things it shows the plan and asks for confirmation by
default; at the prompt, with no scope flag, w/b narrow to worktrees/branches and
a returns to all. Pass -y/--yes to skip the prompt (CI / scripted runs), or -n to
preview without removing. Without -y a non-interactive run refuses rather than
delete unattended. Deletions are recoverable from the reflog. Merge state is
tested against the local base branch, so keep it current (e.g. pull main).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if wtOnly && brOnly {
				return fmt.Errorf("pass only one of --worktrees / --branches (omit both to prune everything)")
			}
			ctx := cmd.Context()
			repo, root, err := openRepo(ctx)
			if err != nil {
				return err
			}
			if base == "" {
				base = repo.DefaultBranch(ctx)
			}
			gone := !keepGone // gone-upstream items are swept unless --keep-gone

			scope := scopeBoth
			scopeFixed := wtOnly || brOnly
			switch {
			case wtOnly:
				scope = scopeWorktrees
			case brOnly:
				scope = scopeBranches
			}

			run := func(w io.Writer, s pruneScope, dry bool) (pruneCounts, error) {
				doWT, doBR := s.phases()
				a, b, err := pruneSweep(ctx, w, repo, root, base, dry, gone, doWT, doBR)
				return pruneCounts{a, b}, err
			}
			return runPruneFlow(cmd, dryRun, yes, scope, scopeFixed, run)
		},
	}
	cmd.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "show what would be removed without removing anything")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt (CI / scripted runs)")
	cmd.Flags().BoolVar(&wtOnly, "worktrees", false, "prune only worktrees")
	cmd.Flags().BoolVar(&brOnly, "branches", false, "prune only branches")
	cmd.Flags().BoolVar(&keepGone, "keep-gone", false, keepGoneUsage)
	cmd.Flags().StringVar(&base, "base", "", "branch to test merges against (default: repo's mainline)")
	return cmd
}

// runPruneFlow is the shared behavior for `prune`: -n previews, -y acts and
// prints the per-item table, and the default shows the plan in a confirm screen
// and acts only on approval — refusing in a non-interactive context rather than
// deleting unattended. With no scope flag the confirm screen's w/b/a keys retarget
// the sweep in place. run writes one line per item to w and returns the counts.
func runPruneFlow(cmd *cobra.Command, dryRun, yes bool, scope pruneScope, scopeFixed bool,
	run func(w io.Writer, s pruneScope, dry bool) (pruneCounts, error)) error {

	out := cmd.OutOrStdout()

	if dryRun {
		c, err := run(out, scope, true)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "%s\n", DimStyle.Render(c.summary(scope, true)))
		return nil
	}

	if yes {
		c, err := run(out, scope, false)
		if err != nil {
			return err
		}
		if c.total() == 0 {
			fmt.Fprintf(out, "%s\n", DimStyle.Render("nothing to prune"))
			return nil
		}
		fmt.Fprintf(out, "%s\n", OkStyle.Render("✓ "+c.summary(scope, false)))
		return nil
	}

	// Destructive path: never proceed without a human at the keyboard.
	if !interactive() {
		return fmt.Errorf("prune deletes worktrees and branches — confirm at a terminal or pass -y/--yes; use -n to preview")
	}

	// Nothing to do for the starting scope (and, when toggling is allowed, the
	// other scopes are subsets of "both") — say so instead of an empty dialog.
	if c, err := run(io.Discard, scope, true); err != nil {
		return err
	} else if c.total() == 0 {
		fmt.Fprintf(out, "%s\n", DimStyle.Render("nothing to prune"))
		return nil
	}

	// preview renders the plan for a scope into text + counts, for the dialog.
	preview := func(s pruneScope) (string, pruneCounts) {
		var buf bytes.Buffer
		c, _ := run(&buf, s, true)
		text := strings.TrimRight(buf.String(), "\n")
		if text == "" {
			text = DimStyle.Render("(nothing in this scope)")
		}
		return text, c
	}

	chosen, proceed := confirmPrune(scope, !scopeFixed, preview)
	if !proceed {
		fmt.Fprintf(out, "%s\n", DimStyle.Render("aborted"))
		return nil
	}

	var real bytes.Buffer
	c, err := run(&real, chosen, false)
	if err != nil {
		return err
	}
	if c.total() == 0 {
		fmt.Fprintf(out, "%s\n", DimStyle.Render("nothing to prune"))
		return nil
	}
	io.Copy(out, &real)
	fmt.Fprintf(out, "%s\n", OkStyle.Render("✓ "+c.summary(chosen, false)))
	return nil
}

// pruneSweep runs the requested phases — worktrees first, then branches — writing
// one line per item to w and returning the removed (or, in dry mode, would-remove)
// counts. The freed branches from the worktree phase are carried into the branch
// phase so they're no longer treated as worktree-attached.
func pruneSweep(ctx context.Context, w io.Writer, repo *gitrepo.Repo, root, base string, dry, gone, doWT, doBR bool) (worktrees, branches int, err error) {
	var freed []string
	if doWT {
		fmt.Fprintln(w, HeaderStyle.Render("worktrees"))
		worktrees, _, freed, err = pruneWorktrees(ctx, w, repo, root, base, dry, gone, false)
		if err != nil {
			return 0, 0, err
		}
	}
	if doBR {
		detached := map[string]bool{}
		for _, b := range freed {
			detached[b] = true
		}
		fmt.Fprintln(w, HeaderStyle.Render("branches"))
		branches, _, err = pruneBranches(ctx, w, repo, base, dry, gone, detached)
		if err != nil {
			return worktrees, 0, err
		}
	}
	return worktrees, branches, nil
}

// keepGoneUsage describes the --keep-gone opt-out, shared by every prune command.
const keepGoneUsage = "keep items whose upstream the remote deleted but whose merge can't be proven (they're removed by default)"
