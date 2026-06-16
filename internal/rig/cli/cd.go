package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/x/term"
	"github.com/rigsmith/rigsmith/core/match"
	"github.com/spf13/cobra"
)

// cdTarget is one navigable destination: a discovered package (or the repo
// root). Name is the package identity, Dir is the absolute directory, and Rel
// is Dir relative to the repo root (used for the picker hint).
type cdTarget struct {
	Name string
	Dir  string
	Rel  string
}

// newCdCmd builds `rig cd [query]`.
//
// It prints a project directory to STDOUT and nothing else there, so a shell
// wrapper can `cd "$(rig cd foo)"`. Every human message — errors, "no match",
// the interactive picker — goes to STDERR, keeping stdout a clean path.
func newCdCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cd [query]",
		Short: "Print a project directory (for the rig shell wrapper to cd into)",
		Long: strings.TrimSpace(`
Print the directory of a project/package to stdout so a shell wrapper can cd
into it; all messages and the picker render to stderr.

With a query it's the best fuzzy match (exact > prefix > substring >
subsequence, name beats path); a query that matches nothing falls back to an
error. Without a query it shows a picker in a terminal, or prints the repo root
when stdout/stderr isn't a TTY.

A subprocess can't change its parent shell's directory, so this needs a shell
wrapper that captures stdout, e.g. in your shell rc:

  rig() {
    if [ "$1" = cd ]; then
      builtin cd "$(command rig cd "${@:2}")"
    else
      command rig "$@"
    fi
  }
`),
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: cdNameCompletion,
		// We print our own messages to stderr and signal failure with errSilent;
		// don't let cobra re-print an (empty) error line.
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			var query string
			if len(args) > 0 {
				query = args[0]
			}
			return runCd(cmd, query)
		},
	}
	return cmd
}

// runCd resolves query to a directory and prints it to stdout. Picker prompts
// and errors go to stderr. Returns a non-nil error (silenced for cobra) only as
// a fallthrough; the explicit exit paths print to stderr and return a sentinel.
func runCd(cmd *cobra.Command, query string) error {
	cwd, _ := os.Getwd()
	root := resolveRoot(cwd)
	targets := buildCdTargets(cdContext(cmd), root, excludeFor(root))

	out := cmd.OutOrStdout()
	errOut := cmd.ErrOrStderr()

	query = strings.TrimSpace(query)
	if query != "" {
		matches := rankCdTargets(targets, query)
		if len(matches) == 1 {
			fmt.Fprintln(out, matches[0].Dir)
			return nil
		}
		if len(matches) == 0 {
			fmt.Fprintf(errOut, "no project matching %q\n", query)
			return errSilent
		}
		// Several matches: pick interactively, else list them and fail.
		if !interactive() {
			fmt.Fprintf(errOut, "multiple projects matching %q:\n", query)
			for _, t := range matches {
				fmt.Fprintf(errOut, "  %s  %s\n", t.Name, t.Rel)
			}
			return errSilent
		}
		dir, err := pickCdTarget(matches)
		if err != nil {
			return errSilent
		}
		fmt.Fprintln(out, dir)
		return nil
	}

	// Bare `rig cd`: picker in a terminal, otherwise the repo root.
	if !interactive() {
		fmt.Fprintln(out, root)
		return nil
	}
	dir, err := pickCdTarget(targets)
	if err != nil {
		return errSilent
	}
	fmt.Fprintln(out, dir)
	return nil
}

// errSilent is returned on the handled exit paths so cobra exits non-zero
// without re-printing a message we already wrote to stderr.
var errSilent = &silentError{}

type silentError struct{}

func (*silentError) Error() string { return "" }

// cdContext returns a non-nil context. cmd.Context() may be nil (the ecosystem
// Detect/Discover now shell out to git and panic on a nil context), so we fall
// back to context.Background().
func cdContext(cmd *cobra.Command) context.Context {
	if cmd != nil {
		if ctx := cmd.Context(); ctx != nil {
			return ctx
		}
	}
	return context.Background()
}

// buildCdTargets enumerates every discoverable package as a cdTarget, plus the
// repo root as "(root)". Packages matching the `exclude` globs are dropped. Dir
// is absolute; Rel is relative to root.
func buildCdTargets(ctx context.Context, root string, exclude []string) []cdTarget {
	var targets []cdTarget
	seen := map[string]bool{}

	for _, t := range discoverWorkspace(ctx, root, exclude) {
		if seen[t.Dir] {
			continue
		}
		seen[t.Dir] = true
		rel, err := filepath.Rel(root, t.Dir)
		if err != nil {
			rel = t.Dir
		}
		targets = append(targets, cdTarget{Name: t.Name, Dir: t.Dir, Rel: rel})
	}
	// Offer "(root)" only when no discovered package already lives at the repo
	// root — otherwise a root-level package would be shadowed and unreachable.
	if !seen[root] {
		targets = append([]cdTarget{{Name: "(root)", Dir: root, Rel: "."}}, targets...)
	}
	return targets
}

// pickCdTarget shows an interactive picker (on stderr) over the targets and
// returns the chosen directory. Caller must have confirmed a TTY.
func pickCdTarget(targets []cdTarget) (string, error) {
	var chosen string
	opts := make([]huh.Option[string], 0, len(targets))
	for _, t := range targets {
		label := t.Name
		if t.Rel != "." {
			label = fmt.Sprintf("%s  (%s)", t.Name, t.Rel)
		}
		opts = append(opts, huh.NewOption(label, t.Dir))
	}
	sel := huh.NewSelect[string]().
		Title("cd to which project?").
		Options(opts...).
		Value(&chosen)
	if err := runHuhSelect(sel); err != nil {
		return "", err
	}
	return chosen, nil
}

// interactive reports whether we can show a picker — both stdin and stderr must
// be a TTY (stderr is where the picker draws; stdout carries the path).
func interactive() bool {
	return term.IsTerminal(os.Stdin.Fd()) && term.IsTerminal(os.Stderr.Fd())
}

// cdNameCompletion completes the [query] arg with discovered project names.
func cdNameCompletion(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	cwd, _ := os.Getwd()
	root := resolveRoot(cwd)
	targets := buildCdTargets(cdContext(cmd), root, excludeFor(root))
	names := make([]string, 0, len(targets))
	for _, t := range targets {
		if t.Name == "(root)" {
			continue
		}
		names = append(names, t.Name)
	}
	sort.Strings(names)
	return names, cobra.ShellCompDirectiveNoFileComp
}

// ---------------------------------------------------------------------------
// Pure matcher — rig's tiered ranking lives in core/match; this adapts cdTarget
// to it (name + short name beat relative path + dir basename).
// ---------------------------------------------------------------------------

// rankCdTargets returns the targets matching query, best first. Path-aware
// (name, short name, relative path, dir basename) and forgiving (exact > prefix
// > substring > subsequence). Ties break by name-match over path-match, then
// deepest directory, then shortest name. Pure.
func rankCdTargets(targets []cdTarget, query string) []cdTarget {
	return match.Rank(targets, query, func(t cdTarget) match.Fields {
		return match.Fields{
			Name:  []string{t.Name, match.ShortName(t.Name)},
			Path:  []string{t.Rel, filepath.Base(t.Dir)},
			Depth: len(t.Dir),
			Tie:   len(t.Name),
		}
	})
}

// shortName, fieldScore, and isSubsequence are package-local aliases for the
// shared matcher in core/match, used across the cli package (doctor, kill, …).
func shortName(name string) string          { return match.ShortName(name) }
func fieldScore(field, q string) int        { return match.Tier(field, q) }
func isSubsequence(needle, hay string) bool { return match.IsSubsequence(needle, hay) }
