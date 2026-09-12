package commands

import (
	"io"
	"os"
	"strings"

	"github.com/rigsmith/rigsmith/core/confkit"
	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/internal/codexrig/guard"
	"github.com/spf13/cobra"
)

// NewGuardCmd builds `codexrig guard`, the PreToolUse hook itself.
//
// Hidden: it is wired up by `codexrig project install` and read by Codex, not by
// a person. It is also deliberately TOTAL — every error path defers, printing
// nothing and exiting 0 — so a bug in the guard can only ever let something
// through, never stop somebody working.
func NewGuardCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "guard",
		Short:  "The PreToolUse hook: keep code changes off a base branch",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			stdin, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), 1<<20))
			if err != nil {
				return nil //nolint:nilerr // fail open
			}
			req, err := guard.Parse(stdin)
			if err != nil || req.Tool == "" {
				return nil
			}
			env := environmentFor(cmd, req)
			if out := guard.Output(guard.Evaluate(req, env)); out != nil {
				cmd.OutOrStdout().Write(append(out, '\n'))
			}
			return nil
		},
	}
}

func environmentFor(cmd *cobra.Command, req guard.Request) guard.Env {
	dir := req.Cwd
	if dir == "" {
		dir, _ = os.Getwd()
	}
	repo, err := gitrepo.Open(cmd.Context(), dir)
	if err != nil {
		return guard.Env{}
	}
	root, err := repo.Toplevel(cmd.Context())
	if err != nil {
		return guard.Env{}
	}
	branch, _ := repo.CurrentBranch(cmd.Context())
	return guard.Env{
		InRepo:   true,
		Root:     root,
		OnBase:   guard.BaseBranches[strings.TrimSpace(branch)],
		Override: overridden(root),
	}
}

// overridden reports the user's explicit "yes, on this branch, on purpose" —
// either for the session or for the repository.
func overridden(root string) bool {
	if confkit.Truthy("CODEXRIG_ALLOW_MAIN") {
		return true
	}
	_, err := os.Stat(root + "/.codex/allow-main")
	return err == nil
}
