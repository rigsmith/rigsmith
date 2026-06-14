package commands

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/rigsmith/rigsmith/core/confkit"
	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/internal/clauderig/guard"
	"github.com/spf13/cobra"
)

// NewGuardCmd builds the `guard` command: clauderig's Claude Code PreToolUse
// hook. It reads the tool-call JSON on stdin and prints a deny decision (or
// nothing) on stdout. Install it with `clauderig hooks install`.
//
// The command is deliberately total: every error path Defers (prints nothing,
// exits 0) so a guard bug can only fail open and never wedge the user's editing.
func NewGuardCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "guard",
		Short:  "PreToolUse hook: require worktrees/PRs, block cwd-moving worktree tools",
		Hidden: true, // invoked by Claude Code, not by hand
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			stdin, err := io.ReadAll(cmd.InOrStdin())
			if err != nil {
				return nil // fail open
			}
			res := decide(cmd.Context(), stdin)
			if out := guard.Output(res); out != nil {
				fmt.Fprintln(cmd.OutOrStdout(), string(out))
			}
			return nil
		},
	}
}

// decide turns a raw PreToolUse payload into a guard Result, resolving the git
// facts the pure engine needs. Any failure to read git state Defers.
func decide(ctx context.Context, stdin []byte) guard.Result {
	req, err := guard.Parse(stdin)
	if err != nil || req.Tool == "" {
		return guard.Result{Decision: guard.Defer}
	}
	env := guard.Env{Home: homeDir()}

	dir := req.Cwd
	if dir == "" {
		dir, _ = os.Getwd()
	}
	repo, err := gitrepo.Open(ctx, dir)
	if err != nil {
		return guard.Evaluate(req, env) // not a repo: only worktree-tool denial applies
	}
	env.InRepo = true
	if root, err := repo.Toplevel(ctx); err == nil {
		env.Root = root
	}
	if branch, err := repo.CurrentBranch(ctx); err == nil {
		env.OnBase = guard.BaseBranches[branch]
	}
	env.Override = overridden(env.Root)

	// Only pay for the staged-file lookup when it can change the verdict.
	if req.Tool == "Bash" && env.OnBase && !env.Override {
		if files, err := repo.CommittableFiles(ctx, hasAllFlag(req.Command)); err == nil {
			env.Committable = files
		}
	}
	return guard.Evaluate(req, env)
}

// overridden reports an explicit opt-out of base-branch protection: a truthy
// CLAUDERIG_ALLOW_MAIN, or a .claude/allow-main sentinel file at the repo root.
func overridden(root string) bool {
	if confkit.Truthy("CLAUDERIG_ALLOW_MAIN") {
		return true
	}
	if root != "" {
		if _, err := os.Stat(filepath.Join(root, ".claude", "allow-main")); err == nil {
			return true
		}
	}
	return false
}

// hasAllFlag reports whether a git commit command stages tracked changes itself
// (-a / --all / a combined short flag like -am), so the guard inspects them too.
func hasAllFlag(command string) bool {
	for _, f := range strings.Fields(command) {
		if f == "--all" {
			return true
		}
		if strings.HasPrefix(f, "-") && !strings.HasPrefix(f, "--") && strings.ContainsRune(f, 'a') {
			return true
		}
	}
	return false
}

func homeDir() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return ""
}
