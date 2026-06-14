package commands

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/mattn/go-isatty"
	"github.com/rigsmith/clauderig/internal/config"
	"github.com/rigsmith/clauderig/internal/gitrepo"
	"github.com/rigsmith/clauderig/internal/worktree"
	"github.com/rigsmith/core/brand"
	"github.com/rigsmith/core/match"
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
	cmd.AddCommand(newWorktreeNewCmd(), newWorktreeListCmd(), newWorktreeOpenCmd(), newWorktreeRemoveCmd(), newWorktreePruneCmd(), newWorktreePickCmd())
	return cmd
}

// newWorktreePickCmd resolves a worktree and prints its path to stdout. It
// powers the `<tool>-wt` dev launchers: the huh UI (when needed) draws on stderr
// so stdout carries only the path the launcher captures. --repo lets a launcher
// invoked from another repo still resolve *this* repo's worktrees.
//
// With a [query] it's the best fuzzy match (exact > prefix > substring >
// subsequence), or a directory path used as-is. With no query it auto-selects
// the lone linked worktree, falls back to the main checkout when there are no
// linked worktrees, or shows an interactive picker when several exist.
func newWorktreePickCmd() *cobra.Command {
	var repoDir string
	cmd := &cobra.Command{
		Use:               "pick [query]",
		Short:             "Resolve or select a worktree and print its path (used by <tool>-wt)",
		Args:              cobra.MaximumNArgs(1),
		Hidden:            true,
		ValidArgsFunction: worktreeCompletion(cobra.ShellCompDirectiveNoFileComp),
		RunE: func(cmd *cobra.Command, args []string) error {
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
			query := ""
			if len(args) == 1 {
				query = strings.TrimSpace(args[0])
			}
			chosen, err := resolveWorktree(wts, query)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), chosen)
			return nil
		},
	}
	cmd.Flags().StringVar(&repoDir, "repo", "", "repo directory whose worktrees to resolve (default: current directory)")
	return cmd
}

// resolveWorktree maps a query (or its absence) to a worktree path. git lists
// the main worktree first, so wts[0] is the main checkout and wts[1:] are the
// linked ones.
func resolveWorktree(wts []gitrepo.Worktree, query string) (string, error) {
	if query == "" {
		linked := wts[1:]
		switch len(linked) {
		case 0:
			return wts[0].Path, nil // no linked worktrees → main (same as -dev)
		case 1:
			return linked[0].Path, nil // exactly one → auto-select
		default:
			return pickWorktree(wts)
		}
	}
	// A directory path wins outright — lets you point at any checkout.
	if fi, err := os.Stat(query); err == nil && fi.IsDir() {
		return filepath.Abs(query)
	}
	ranked := match.Rank(wts, query, func(w gitrepo.Worktree) match.Fields {
		return match.Fields{
			Name: []string{w.Branch, match.ShortName(w.Branch)},
			Path: []string{filepath.Base(w.Path)},
			// No depth preference for worktrees; ties go to the shortest
			// (closest) branch name.
			Tie: len(w.Branch),
		}
	})
	if len(ranked) == 0 {
		return "", fmt.Errorf("no worktree matching %q", query)
	}
	return ranked[0].Path, nil
}

// worktreeCompletion builds a Cobra ValidArgsFunction that completes the first
// positional arg with this repo's worktree branch names, each described by its
// path. It's what makes `open`/`rm`/`pick` a single <Tab> away from picking an
// existing checkout. dir is the directive returned alongside the names: pass
// ShellCompDirectiveNoFileComp for a branch-only arg (rm/pick), or
// ShellCompDirectiveFilterDirs for one that also accepts a path (open), so
// directory completion still works. Any failure (not in a repo, no worktrees)
// degrades to "no completions" rather than erroring — completion must never get
// in the user's way.
func worktreeCompletion(dir cobra.ShellCompDirective) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		repo, _, err := openRepo(cmd.Context())
		if err != nil {
			return nil, dir
		}
		wts, err := repo.WorktreeList(cmd.Context())
		if err != nil {
			return nil, dir
		}
		return worktreeCompletions(wts), dir
	}
}

// worktreeCompletions turns a worktree list into Cobra completion candidates in
// "value\tdescription" form — the branch name (and its short name, when it
// differs) keyed to the worktree's path. Those are the same two name forms
// resolveWorktree ranks against, so anything that completes also resolves.
// Detached worktrees (empty branch) contribute nothing. Pure, so it's unit
// tested directly.
func worktreeCompletions(wts []gitrepo.Worktree) []string {
	seen := map[string]bool{}
	var comps []string
	add := func(name, path string) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		comps = append(comps, name+"\t"+path)
	}
	for _, wt := range wts {
		add(wt.Branch, wt.Path)
		add(match.ShortName(wt.Branch), wt.Path)
	}
	return comps
}

// pickWorktree shows the filterable huh worktree picker and returns the chosen
// path. Requires a TTY on stderr (stdout carries the result).
func pickWorktree(wts []gitrepo.Worktree) (string, error) {
	if !pickerTTY() {
		return "", fmt.Errorf("multiple worktrees and no terminal for the picker; pass a branch or path")
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
	err := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().Title("Run from which worktree?").Options(opts...).Filtering(true).Value(&chosen),
	)).WithTheme(brand.Theme(brand.AccentClaude)).Run()
	if err != nil {
		return "", err
	}
	return chosen, nil
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
	var open, noOpen bool
	cmd := &cobra.Command{
		Use:   "new <branch>",
		Short: "Create a worktree (and branch) at a sibling path",
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
			// Auto-open is opt-in: off by default, enabled via the worktree.autoOpen
			// config. The --open/--no-open flags override that per run. When we skip,
			// openReview still prints the review hint.
			openWindow := false
			if cfg, err := config.LoadOrDefault(); err == nil {
				openWindow = cfg.WorktreeAutoOpen()
			}
			if cmd.Flags().Changed("open") {
				openWindow = open
			}
			if noOpen {
				openWindow = false
			}
			openReview(cmd, path, !openWindow)
			fmt.Fprintln(out, DimStyle.Render("  This window stays put. Edit there by absolute path; run git via:"))
			fmt.Fprintf(out, "  %s\n", DimStyle.Render("git -C "+path+" add/commit/push  →  then open a PR"))
			return nil
		},
	}
	cmd.Flags().StringVar(&base, "base", "", "branch to fork from (default: repo's mainline)")
	cmd.Flags().BoolVar(&open, "open", false, "open the worktree in a new VS Code window for review")
	cmd.Flags().BoolVar(&noOpen, "no-open", false, "don't open a window even if worktree.autoOpen is set")
	cmd.MarkFlagsMutuallyExclusive("open", "no-open")
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
		Use:               "open <branch|path>",
		Short:             "Open a worktree in a new VS Code window (for review/diff)",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: worktreeCompletion(cobra.ShellCompDirectiveFilterDirs),
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
		Use:               "rm <branch>",
		Aliases:           []string{"remove"},
		Short:             "Remove a worktree (its branch is kept)",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: worktreeCompletion(cobra.ShellCompDirectiveNoFileComp),
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

// newWorktreePruneCmd sweeps the repo's linked worktrees and removes each one
// that is safe to drop: clean (no uncommitted changes) AND merged (its branch
// is fully contained in the base). Removing a worktree keeps the branch and its
// commits, so a false positive costs only a `worktree new` to recreate — the
// genuinely destructive --delete-branches is opt-in. It acts unattended (no
// prompt), which is what lets a SessionStart hook call it to reap the orphans
// left when a session ends without a clean exit.
func newWorktreePruneCmd() *cobra.Command {
	var dryRun, deleteBranches, gone bool
	var base string
	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Remove worktrees whose branch is clean and already merged",
		Long: `Sweep this repo's linked worktrees and remove each one that is both:

  • clean   no uncommitted or untracked changes
  • merged  its branch is fully contained in the base branch
            (detects squash-merges as well as ordinary merges)

With --gone a clean worktree is also removed when its branch's upstream was
deleted on the remote. The primary checkout and the worktree you're running from
are never touched, and dirty or unmerged worktrees are skipped with a reason.
Removing a worktree keeps its branch unless --delete-branches is given. Merge
state is tested against the local base branch, so keep it current (e.g. pull
main) for accurate results.`,
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
			removed, kept, _, err := pruneWorktrees(ctx, out, repo, root, base, dryRun, gone, deleteBranches)
			if err != nil {
				return err
			}
			verb := "removed"
			if dryRun {
				verb = "to remove"
			}
			fmt.Fprintf(out, "%s\n", DimStyle.Render(fmt.Sprintf("%d %s, %d kept", removed, verb, kept)))
			return nil
		},
	}
	cmd.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "show what would be removed without removing anything")
	cmd.Flags().BoolVarP(&deleteBranches, "delete-branches", "b", false, "also delete the local branch of each removed worktree")
	cmd.Flags().BoolVar(&gone, "gone", false, "also remove clean worktrees whose branch's upstream was deleted on the remote")
	cmd.Flags().StringVar(&base, "base", "", "branch to test merges against (default: repo's mainline)")
	return cmd
}

// pruneWorktrees removes the repo's linked worktrees that are clean and either
// merged into base or (with gone) whose branch's upstream the remote deleted. It
// prints one line per worktree and returns the removed/kept counts plus freed —
// the branch names whose worktrees were removed (or, in dry-run, would be), which
// the combined sweep uses so the branch phase no longer treats them as attached.
// The caller prints the summary line. root is this session's worktree, which —
// like the primary checkout and the base — is never touched.
func pruneWorktrees(ctx context.Context, out io.Writer, repo *gitrepo.Repo, root, base string, dryRun, gone, deleteBranches bool) (removed, kept int, freed []string, err error) {
	wts, err := repo.WorktreeList(ctx)
	if err != nil {
		return 0, 0, nil, err
	}
	// Clear stale records for worktree dirs removed by hand, so the list we act
	// on reflects reality. Harmless on existing checkouts.
	if !dryRun {
		_ = repo.WorktreePruneMeta(ctx)
	}
	goneSet := map[string]bool{}
	if gone {
		if brs, err := repo.LocalBranches(ctx); err == nil {
			for _, b := range brs {
				if b.Gone {
					goneSet[b.Name] = true
				}
			}
		}
	}
	skip := func(label, reason string) {
		kept++
		fmt.Fprintf(out, "%s %s  %s\n", DimStyle.Render("•"), HeaderStyle.Render(label), DimStyle.Render(reason))
	}
	for _, wt := range wts {
		// Never the primary checkout, the base-branch worktree, or the one we're
		// standing in (root is this session's worktree).
		if sameDir(wt.Path, root) || wt.Branch == base {
			continue
		}
		if wt.Branch == "" {
			skip("(detached)", "detached HEAD — skipped")
			continue
		}
		label := wt.Branch
		clean, err := repo.WorktreeClean(ctx, wt.Path)
		if err != nil {
			skip(label, "couldn't read status — skipped")
			continue
		}
		if !clean {
			skip(label, "uncommitted changes — skipped")
			continue
		}
		merged, err := repo.IsMerged(ctx, wt.Branch, base)
		if err != nil {
			skip(label, "couldn't check merge state — skipped")
			continue
		}
		reason := "merged"
		switch {
		case merged:
		case goneSet[wt.Branch]:
			reason = "upstream gone"
		default:
			skip(label, fmt.Sprintf("not merged into %s — skipped", base))
			continue
		}
		if dryRun {
			removed++
			freed = append(freed, wt.Branch)
			fmt.Fprintf(out, "%s %s  %s\n", WarnStyle.Render("⤳"), HeaderStyle.Render(label), DimStyle.Render("would remove "+wt.Path+" ("+reason+")"))
			continue
		}
		if err := repo.WorktreeRemove(ctx, wt.Path, false); err != nil {
			skip(label, "remove failed: "+err.Error())
			continue
		}
		removed++
		freed = append(freed, wt.Branch)
		fmt.Fprintf(out, "%s %s  %s\n", OkStyle.Render("✓"), HeaderStyle.Render(label), DimStyle.Render("removed "+wt.Path+" ("+reason+")"))
		if deleteBranches {
			if err := repo.DeleteBranch(ctx, wt.Branch, false); err != nil {
				fmt.Fprintf(out, "  %s\n", WarnStyle.Render("kept branch "+wt.Branch+": "+err.Error()))
			} else {
				fmt.Fprintf(out, "  %s\n", DimStyle.Render("deleted branch "+wt.Branch))
			}
		}
	}
	return removed, kept, freed, nil
}

// sameDir reports whether two paths point at the same directory, resolving both
// to absolute, symlink-free form first. That matters on macOS, where temp dirs
// (and /var) live behind /private symlinks, so a raw string compare of a git
// path against a resolved one would miss.
func sameDir(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return resolveDir(a) == resolveDir(b)
}

func resolveDir(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	if real, err := filepath.EvalSymlinks(p); err == nil {
		return real
	}
	return filepath.Clean(p)
}

// openReview opens path in a review window using the configured opener (default
// `code -n`), or prints the command to run when skip is set or the opener isn't
// on PATH. skip carries the caller's decision (the --no-open flag and, for
// `new`, the worktree.autoOpen config); the opener choice comes from config.
func openReview(cmd *cobra.Command, path string, skip bool) {
	out := cmd.OutOrStdout()
	openCmd := config.DefaultWorktreeOpenCmd
	if cfg, err := config.LoadOrDefault(); err == nil {
		openCmd = cfg.WorktreeOpenCmd()
	}
	hint := worktree.QuoteCmd(openCmd, path)
	if skip || !worktree.OpenerAvailable(openCmd) {
		fmt.Fprintf(out, "  %s\n", DimStyle.Render("review it: "+hint))
		return
	}
	if err := worktree.Open(cmd.Context(), openCmd, path); err != nil {
		fmt.Fprintf(out, "  %s\n", WarnStyle.Render("couldn't open a review window; run: "+hint))
		return
	}
	fmt.Fprintf(out, "  %s\n", DimStyle.Render("opened in a new window for review"))
}
