package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/spf13/cobra"
)

// newWorktreeCdCmd builds `rig wt cd [query]`: print a worktree's directory to
// stdout so the rig shell wrapper can cd into it — `rig cd` narrowed to this
// repo's worktrees. Like `rig cd`, every human message and the picker draw on
// stderr, keeping stdout a clean path, and handled exits return errSilent so
// cobra doesn't re-print.
func newWorktreeCdCmd() *cobra.Command {
	var repoDir string
	cmd := &cobra.Command{
		Use:   "cd [query]",
		Short: "Print a worktree directory (for the rig shell wrapper to cd into)",
		Long: strings.TrimSpace(`
Print a worktree's directory to stdout so the rig shell wrapper can cd into it —
like ` + "`rig cd`" + ` but limited to this repo's worktrees. Messages and the picker
render to stderr.

With a query it's the best fuzzy match over branch names and paths (exact >
prefix > substring > subsequence), or a directory path used as-is; a query that
matches nothing is an error. Without a query it shows a picker in a terminal, or
prints the main worktree when stdout/stderr isn't a TTY.

Needs the rig shell wrapper installed by ` + "`rig setup`" + ` — it captures stdout
for ` + "`rig wt cd`" + ` alongside ` + "`rig cd`" + ` and cds the parent shell there.
`),
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: worktreeCompletion(cobra.ShellCompDirectiveNoFileComp),
		// We print our own messages to stderr and signal failure with errSilent;
		// don't let cobra re-print an (empty) error line.
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			query := ""
			if len(args) > 0 {
				query = args[0]
			}
			return runWtCd(cmd, repoDir, query)
		},
	}
	cmd.Flags().StringVar(&repoDir, "repo", "", "repo directory whose worktrees to use (default: current directory)")
	return cmd
}

// runWtCd resolves query to a worktree directory and prints it to stdout. Picker
// prompts and errors go to stderr. Mirrors runCd's contract (errSilent on the
// handled exit paths), but its candidates are worktrees, not packages.
func runWtCd(cmd *cobra.Command, repoDir, query string) error {
	_, wts, err := worktreesFor(cmd.Context(), repoDir)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	errOut := cmd.ErrOrStderr()

	query = strings.TrimSpace(query)
	if query != "" {
		// A directory path wins outright — lets you point at any checkout, the
		// same escape hatch resolveWorktree offers.
		if fi, err := os.Stat(query); err == nil && fi.IsDir() {
			abs, err := filepath.Abs(query)
			if err != nil {
				return err
			}
			fmt.Fprintln(out, abs)
			return nil
		}
		matches := rankWorktrees(wts, query)
		switch len(matches) {
		case 1:
			fmt.Fprintln(out, matches[0].Path)
			return nil
		case 0:
			fmt.Fprintf(errOut, "no worktree matching %q\n", query)
			return errSilent
		}
		// Several matches: pick interactively, else list them and fail.
		if !interactive() {
			fmt.Fprintf(errOut, "multiple worktrees matching %q:\n", query)
			for _, wt := range matches {
				fmt.Fprintf(errOut, "  %s  %s\n", worktreeBranchLabel(wt), wt.Path)
			}
			return errSilent
		}
		dir, err := pickWorktreeTitled(matches, "cd to which worktree?")
		if err != nil {
			return errSilent
		}
		fmt.Fprintln(out, dir)
		return nil
	}

	// Bare `rig wt cd`: picker in a terminal, otherwise the main worktree.
	if !interactive() {
		fmt.Fprintln(out, wts[0].Path) // git lists the main worktree first
		return nil
	}
	dir, err := pickWorktreeTitled(wts, "cd to which worktree?")
	if err != nil {
		return errSilent
	}
	fmt.Fprintln(out, dir)
	return nil
}

// worktreeBranchLabel names a worktree's branch for display, falling back to
// "(detached)" when it has no branch checked out.
func worktreeBranchLabel(w gitrepo.Worktree) string {
	if w.Branch == "" {
		return "(detached)"
	}
	return w.Branch
}
