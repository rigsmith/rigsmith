package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/rigsmith/rigsmith/core/copytree"
	"github.com/spf13/cobra"
)

// newCopyCmd builds the top-level `copy` (alias `cp`): a one-shot, detached copy
// of the current repository's working tree into a fresh directory. Unlike
// `worktree new`, the result has no link back to this repo — it's a standalone
// snapshot, handy for throwaway experiments, scaffolding a new project from an
// old one, or a quick backup.
//
// The copy reuses the same discovery rules as the rest of rig: it skips the
// dependency trees and build output rig always ignores (node_modules, vendor,
// dist, …) and the repo's .gitignore'd paths. The .git directory is left out by
// default; --git copies it verbatim so the destination is a full independent
// repository (history and all).
//
// Copy never overwrites: the destination must be a new path or an empty
// directory, so an existing populated folder is refused outright rather than
// merged into or clobbered.
func newCopyCmd() *cobra.Command {
	var withGit bool
	cmd := &cobra.Command{
		Use:     "copy <dest>",
		Aliases: []string{"cp"},
		Short:   "Copy this repo's tree to a new folder (skips node_modules/.git; --git keeps git)",
		Long: `Copy the current repository's working tree into a fresh directory.

Skipped, matching the rest of rig: dependency trees and build output
(node_modules, vendor, dist, bin, obj, target, .next, .turbo) and the repo's
.gitignore'd paths. The result is a detached copy with no link back to this
repo — not a worktree.

  rig copy ../my-app-copy         clean tree, no git history
  rig copy ../my-app-copy --git   include .git → a full independent repo

The destination must be a new path or an empty directory; copy refuses to write
into a populated folder.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			_, root, err := openRepo(ctx)
			if err != nil {
				return err
			}

			dest, err := filepath.Abs(args[0])
			if err != nil {
				return err
			}
			if err := ensureWritableDest(dest); err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			st, err := copytree.Copy(root, dest, withGit)
			if err != nil {
				return err
			}

			fmt.Fprintf(out, "%s\n", OkStyle.Render("✓ copied to "+dest))
			summary := fmt.Sprintf("%d files, %d dirs, %s", st.Files, st.Dirs, humanBytes(st.Bytes))
			if st.GitIncluded {
				summary += " (with .git)"
			}
			fmt.Fprintf(out, "%s\n", DimStyle.Render(summary))
			return nil
		},
	}
	cmd.Flags().BoolVar(&withGit, "git", false, "include the .git directory (copy is a full independent repository)")
	return cmd
}

// ensureWritableDest enforces copy's no-clobber contract: the destination must
// be absent, or an existing empty directory. A populated directory or an
// existing file is refused — copy never merges into or overwrites a populated
// path.
func ensureWritableDest(dest string) error {
	info, err := os.Stat(dest)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("destination already exists and is a file: %s", dest)
	}
	entries, err := os.ReadDir(dest)
	if err != nil {
		return err
	}
	if len(entries) > 0 {
		return fmt.Errorf("destination already exists and is not empty: %s — copy needs a new or empty directory", dest)
	}
	return nil
}

// humanBytes renders a byte count in the largest unit that keeps it under 1024,
// for the copy summary line.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
