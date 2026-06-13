package commands

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/charmbracelet/huh"
	"github.com/mattn/go-isatty"
	"github.com/rigsmith/clauderig/internal/gitrepo"
	"github.com/rigsmith/clauderig/internal/worktree"
	"github.com/spf13/cobra"
)

// NewWorktreeCmd builds the `worktree` command group: create/list/open/remove
// git worktrees the disciplined way. A worktree is a *sibling* checkout opened in
// its own VS Code window — this session's working directory (and its chat
// history) never moves, which is exactly what the EnterWorktree guard prevents
// Claude from doing the messy way.
func NewWorktreeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "worktree",
		Aliases: []string{"wt"},
		Short:   "Create/list/open git worktrees as sibling checkouts (never moves this session)",
	}
	cmd.AddCommand(newWorktreeNewCmd(), newWorktreeListCmd(), newWorktreeOpenCmd(), newWorktreeRemoveCmd(), newWorktreePickCmd())
	return cmd
}

// newWorktreePickCmd shows an interactive picker over the repo's worktrees and
// prints the chosen worktree's path to stdout. It powers the `<tool>-wt` dev
// launchers (run with no branch argument): the huh UI draws on stderr so stdout
// carries only the path the launcher captures. --repo lets a launcher invoked
// from another repo still list *this* repo's worktrees.
func newWorktreePickCmd() *cobra.Command {
	var repoDir string
	cmd := &cobra.Command{
		Use:    "pick",
		Short:  "Interactively select a worktree and print its path (used by <tool>-wt)",
		Args:   cobra.NoArgs,
		Hidden: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			dir := repoDir
			if dir == "" {
				cwd, err := os.Getwd()
				if err != nil {
					return err
				}
				dir = cwd
			}
			repo, err := gitrepo.Open(ctx, dir)
			if err != nil {
				return fmt.Errorf("not inside a git repository")
			}
			wts, err := repo.WorktreeList(ctx)
			if err != nil {
				return err
			}
			if len(wts) == 0 {
				return fmt.Errorf("no worktrees found")
			}
			if !pickerTTY() {
				return fmt.Errorf("no terminal for the picker; pass a branch or path explicitly")
			}
			opts := make([]huh.Option[string], 0, len(wts))
			for _, wt := range wts {
				branch := wt.Branch
				if branch == "" {
					branch = "(detached)"
				}
				label := fmt.Sprintf("%s  %s", HeaderStyle.Render(branch), DimStyle.Render(wt.Path))
				opts = append(opts, huh.NewOption(label, wt.Path))
			}
			var chosen string
			err = huh.NewForm(huh.NewGroup(
				huh.NewSelect[string]().Title("Run from which worktree?").Options(opts...).Value(&chosen),
			)).Run()
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), chosen)
			return nil
		},
	}
	cmd.Flags().StringVar(&repoDir, "repo", "", "repo directory whose worktrees to list (default: current directory)")
	return cmd
}

// pickerTTY reports whether we can show the worktree picker. Unlike the shared
// interactive(), it checks stderr (where huh draws) rather than stdout, because
// callers capture stdout for the chosen path.
func pickerTTY() bool {
	return isatty.IsTerminal(os.Stderr.Fd()) && isatty.IsTerminal(os.Stdin.Fd())
}

// openRepo opens the git repo containing the current directory.
func openRepo(ctx context.Context) (*gitrepo.Repo, string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, "", err
	}
	repo, err := gitrepo.Open(ctx, cwd)
	if err != nil {
		return nil, "", fmt.Errorf("not inside a git repository")
	}
	root, err := repo.Toplevel(ctx)
	if err != nil {
		return nil, "", err
	}
	return repo, root, nil
}

func newWorktreeNewCmd() *cobra.Command {
	var base string
	var noOpen bool
	cmd := &cobra.Command{
		Use:   "new <branch>",
		Short: "Create a worktree (and branch) at a sibling path, then open it for review",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			branch := args[0]
			repo, root, err := openRepo(ctx)
			if err != nil {
				return err
			}
			path := worktree.PathFor(root, branch)
			if _, err := os.Stat(path); err == nil {
				return fmt.Errorf("worktree path already exists: %s", path)
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			create := !repo.BranchExists(ctx, branch)
			if base == "" {
				base = repo.DefaultBranch(ctx)
			}
			if err := repo.WorktreeAdd(ctx, path, branch, base, create); err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			verb := "checked out"
			if create {
				verb = fmt.Sprintf("created off %s", base)
			}
			fmt.Fprintf(out, "%s worktree for %s (%s)\n", OkStyle.Render("✓"), HeaderStyle.Render(branch), verb)
			fmt.Fprintf(out, "  %s\n", path)
			openReview(cmd, path, noOpen)
			fmt.Fprintln(out, DimStyle.Render("  This window stays put. Edit there by absolute path; run git via:"))
			fmt.Fprintf(out, "  %s\n", DimStyle.Render("git -C "+path+" add/commit/push  →  then open a PR"))
			return nil
		},
	}
	cmd.Flags().StringVar(&base, "base", "", "branch to fork from (default: repo's mainline)")
	cmd.Flags().BoolVar(&noOpen, "no-open", false, "don't open the worktree in a new VS Code window")
	return cmd
}

func newWorktreeListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List this repo's worktrees",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			repo, _, err := openRepo(ctx)
			if err != nil {
				return err
			}
			wts, err := repo.WorktreeList(ctx)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			for _, wt := range wts {
				branch := wt.Branch
				if branch == "" {
					branch = "(detached)"
				}
				fmt.Fprintf(out, "%s  %s\n", HeaderStyle.Render(branch), DimStyle.Render(wt.Path))
			}
			return nil
		},
	}
}

func newWorktreeOpenCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "open <branch|path>",
		Short: "Open a worktree in a new VS Code window (for review/diff)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			_, root, err := openRepo(ctx)
			if err != nil {
				return err
			}
			path := args[0]
			if _, err := os.Stat(path); err != nil {
				path = worktree.PathFor(root, args[0])
			}
			if _, err := os.Stat(path); err != nil {
				return fmt.Errorf("no such worktree: %s", path)
			}
			openReview(cmd, path, false)
			return nil
		},
	}
}

func newWorktreeRemoveCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:     "rm <branch>",
		Aliases: []string{"remove"},
		Short:   "Remove a worktree (its branch is kept)",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			repo, root, err := openRepo(ctx)
			if err != nil {
				return err
			}
			path := worktree.PathFor(root, args[0])
			if err := repo.WorktreeRemove(ctx, path, force); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s removed %s\n", OkStyle.Render("✓"), path)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&force, "force", "f", false, "remove even if the worktree has changes")
	return cmd
}

// openReview launches path in a new VS Code window, or prints the command to run
// when `code` isn't on PATH or the caller asked to skip it.
func openReview(cmd *cobra.Command, path string, skip bool) {
	out := cmd.OutOrStdout()
	if skip || !worktree.VSCodeAvailable() {
		fmt.Fprintf(out, "  %s\n", DimStyle.Render("review it: code -n "+path))
		return
	}
	if err := worktree.OpenInVSCode(cmd.Context(), path); err != nil {
		fmt.Fprintf(out, "  %s\n", WarnStyle.Render("couldn't open VS Code; run: code -n "+path))
		return
	}
	fmt.Fprintf(out, "  %s\n", DimStyle.Render("opened in a new VS Code window for review"))
}
