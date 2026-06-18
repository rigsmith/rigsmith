package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/huh"
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

// newPruneCmd builds the top-level `prune` (alias `tidy`): one sweep that clears
// both halves of a finished branch. It runs the worktree sweep first — git won't
// delete a branch that's checked out — then the branch sweep, which picks up the
// branches just freed along with any others that are merged or whose upstream
// the remote deleted. For a worktree+branch pair that landed
// together, this is the single "tidy up after a merged PR" command;
// `worktree prune` and `branch prune` remain for one side at a time.
//
// Unlike those targeted commands, prune deletes branches, so it ALWAYS requires
// an interactive confirmation: it previews the plan, then asks at a real
// terminal before touching anything. There is deliberately no flag to skip the
// prompt — in a non-interactive context (a script, hook, or piped run) prune
// fails outright rather than delete unattended. Use -n to preview anywhere, or
// the unattended `worktree prune` / `branch prune` for automation.
func newPruneCmd() *cobra.Command {
	var dryRun, keepGone bool
	var base string
	cmd := &cobra.Command{
		Use:     "prune",
		Aliases: []string{"tidy"},
		Short:   "Remove merged/done worktrees and their branches in one sweep (interactive)",
		Long: `Tidy up after merged PRs: remove finished worktrees and branches together.

Two phases run in order (worktrees first, since a checked-out branch can't be
deleted):

  1. worktrees — remove each clean worktree whose branch is merged into base
                 or whose upstream the remote deleted (--keep-gone to keep those)
  2. branches  — delete each branch that is merged or whose upstream is gone,
                 including the ones whose worktree phase 1 just removed

A deleted upstream is the strongest "done" signal — it also catches squash-merges
that the local patch-id check can't prove — so gone-upstream items are removed by
default; pass --keep-gone to keep them. The current branch/worktree, the base, and
dirty or unmerged checkouts are never
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
			gone := !keepGone // gone-upstream items are swept unless --keep-gone

			if dryRun {
				w, b, err := pruneSweep(ctx, out, repo, root, base, true, gone)
				if err != nil {
					return err
				}
				fmt.Fprintf(out, "%s\n", DimStyle.Render(fmt.Sprintf("%d worktrees, %d branches to remove", w, b)))
				return nil
			}

			// Destructive path: never proceed without a human at the keyboard.
			// Fail before doing any work in a non-interactive context.
			if !interactive() {
				return fmt.Errorf("prune deletes branches and must be confirmed at a terminal — it won't run non-interactively; use -n (or prune list) to preview, or worktree prune / branch prune for unattended cleanup")
			}

			// Capture the plan into a buffer and show it *inside* the confirm dialog,
			// as a Note above the Confirm in one huh form. The whole thing is then a
			// single bubbletea view that owns and clears its own lines. (Printing the
			// plan first and then launching a separate form makes the form repaint
			// over the already-printed lines, leaving stale tails bleeding through
			// from longer scrollback — the misdisplay this avoids.)
			var plan bytes.Buffer
			planW, planB, err := pruneSweep(ctx, &plan, repo, root, base, true, gone)
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
			)).WithKeyMap(huhEscKeyMap()).WithTheme(rigTheme()).Run(); err != nil || !proceed {
				fmt.Fprintf(out, "%s\n", DimStyle.Render("aborted"))
				return nil
			}

			// Act for real into a buffer; on a clean run (results match the plan
			// the user just approved) print a terse summary, otherwise flush the
			// detail so any failure or drift is visible.
			var real bytes.Buffer
			w, b, err := pruneSweep(ctx, &real, repo, root, base, false, gone)
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
	cmd.Flags().BoolVar(&keepGone, "keep-gone", false, keepGoneUsage)
	cmd.Flags().StringVar(&base, "base", "", "branch to test merges against (default: repo's mainline)")
	cmd.AddCommand(newPruneListCmd())
	return cmd
}

// pruneSweep runs both phases — worktrees first, then branches — writing one
// line per item to w and returning the removed (or, in dry mode, would-remove)
// counts. The freed branches from phase 1 are carried into phase 2 so they're no
// longer treated as worktree-attached. Shared by the prune action and its list
// subcommand.
func pruneSweep(ctx context.Context, w io.Writer, repo *gitrepo.Repo, root, base string, dry, gone bool) (worktrees, branches int, err error) {
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

// keepGoneUsage describes the --keep-gone opt-out, shared by every prune command.
const keepGoneUsage = "keep items whose upstream the remote deleted but whose merge can't be proven (they're removed by default)"

// newPruneListCmd is the read-only companion to prune: it shows the same plan —
// the worktrees and branches that would be removed (including gone-upstream ones,
// unless --keep-gone) and what's skipped and why — without touching anything.
// Equivalent to `prune -n`, surfaced as a subcommand for parity with
// `worktree list` / `branch list`.
func newPruneListCmd() *cobra.Command {
	var keepGone bool
	var base string
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "Show what prune would remove (worktrees + branches), without removing",
		Args:    cobra.NoArgs,
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
			w, b, err := pruneSweep(ctx, out, repo, root, base, true, !keepGone)
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "%s\n", DimStyle.Render(fmt.Sprintf("%d worktrees, %d branches to remove", w, b)))
			return nil
		},
	}
	cmd.Flags().BoolVar(&keepGone, "keep-gone", false, keepGoneUsage)
	cmd.Flags().StringVar(&base, "base", "", "branch to test merges against (default: repo's mainline)")
	return cmd
}
