// Package worktree holds rigsmith's worktree-layout convention and its
// review-window launcher (VS Code by default, configurable). Worktrees live in a
// sibling directory of the repo (never inside it),
// so they don't clutter the primary checkout's file tree and each gets its own
// folder path — which is also its own Claude Code chat-history bucket when opened
// in a separate window for review.
package worktree

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// Sanitize turns a branch name into a single path segment (slashes and the like
// become dashes), so `feat/x` lands in one directory, not a nested `feat/x`.
func Sanitize(branch string) string {
	r := strings.NewReplacer("/", "-", "\\", "-", ":", "-", " ", "-")
	return strings.Trim(r.Replace(branch), "-")
}

// PathFor returns the sibling-directory path for a branch's worktree:
//
//	<parent>/<repo>-worktrees/<branch>
//
// e.g. /Users/john/Git/rigsmith on branch feat/x →
// /Users/john/Git/rigsmith-worktrees/feat-x.
func PathFor(repoRoot, branch string) string {
	repoRoot = filepath.Clean(repoRoot)
	parent := filepath.Dir(repoRoot)
	name := filepath.Base(repoRoot)
	return filepath.Join(parent, name+"-worktrees", Sanitize(branch))
}

// OpenerAvailable reports whether the open command's program (openCmd[0]) is on
// PATH. An empty command is never available.
func OpenerAvailable(openCmd []string) bool {
	if len(openCmd) == 0 {
		return false
	}
	_, err := exec.LookPath(openCmd[0])
	return err == nil
}

// Open runs openCmd with path appended as the final argument — e.g.
// {"code","-n"} opens `code -n <path>`, a new VS Code window. The window is for
// human review/diff; opened on its own folder path it carries its own chat
// history, so the primary window's conversation is never disturbed. openCmd is
// run directly (no shell), so its parts are passed through verbatim.
func Open(ctx context.Context, openCmd []string, path string) error {
	if len(openCmd) == 0 {
		return fmt.Errorf("no open command configured")
	}
	args := append(append([]string{}, openCmd[1:]...), path)
	return exec.CommandContext(ctx, openCmd[0], args...).Run()
}

// QuoteCmd renders an open command + path the way you'd type it, for the
// fallback hint shown when the opener isn't on PATH or auto-open is off.
func QuoteCmd(openCmd []string, path string) string {
	return strings.Join(append(append([]string{}, openCmd...), path), " ")
}
