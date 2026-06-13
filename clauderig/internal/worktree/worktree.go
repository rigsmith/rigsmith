// Package worktree holds clauderig's worktree-layout convention and its VS Code
// launcher. Worktrees live in a sibling directory of the repo (never inside it),
// so they don't clutter the primary checkout's file tree and each gets its own
// folder path — which is also its own Claude Code chat-history bucket when opened
// in a separate window for review.
package worktree

import (
	"context"
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

// VSCodeAvailable reports whether the `code` CLI is on PATH.
func VSCodeAvailable() bool {
	_, err := exec.LookPath("code")
	return err == nil
}

// OpenInVSCode opens path in a new VS Code window (`code -n <path>`). The new
// window is for human review/diff; it carries its own chat history, so the
// primary window's conversation is never disturbed.
func OpenInVSCode(ctx context.Context, path string) error {
	return exec.CommandContext(ctx, "code", "-n", path).Run()
}
