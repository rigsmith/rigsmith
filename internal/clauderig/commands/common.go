package commands

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/mattn/go-isatty"
	"github.com/rigsmith/rigsmith/core/gitrepo"
)

// openRepo opens the git repo containing the current directory and returns it
// with its toplevel path. guide/hooks use it to locate the repo root; the
// worktree commands themselves now live in rig.
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

// interactive reports whether we're attached to a terminal (so it's safe to
// launch git mergetool / prompt). Hooks run non-interactively and must not.
func interactive() bool { return Interactive() }

// Interactive reports whether both stdin and stdout are real terminals — the
// shared gate for any prompt and for landing bare `clauderig` on the dashboard.
func Interactive() bool {
	return isatty.IsTerminal(os.Stdout.Fd()) && isatty.IsTerminal(os.Stdin.Fd())
}

// planned prints a scaffolded command's intended behaviour plus a clear
// not-yet-implemented marker, so the skeleton is runnable and self-documenting
// while the real logic lands incrementally.
func planned(w io.Writer, title string, lines ...string) {
	fmt.Fprintln(w, HeaderStyle.Render(title))
	for _, l := range lines {
		fmt.Fprintf(w, "  %s\n", l)
	}
	fmt.Fprintf(w, "\n  %s\n", DimStyle.Render("(not yet implemented)"))
}
