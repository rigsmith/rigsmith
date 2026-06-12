package commands

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/rigsmith/clauderig/internal/config"
	"github.com/rigsmith/clauderig/internal/gitrepo"
	"github.com/spf13/cobra"
)

// NewPullCmd builds the `pull` command — fetch the latest into the local staging
// repo without writing to ~/.claude. It is the SessionStart hook target: safe and
// non-interactive, it never touches the live tree and swallows network errors so
// it can never block a session from starting (the hook-safety rule).
func NewPullCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pull",
		Short: "Fetch latest into the local staging repo (no write to ~/.claude)",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			out := cmd.OutOrStdout()

			cfg, err := config.LoadOrDefault()
			if err != nil {
				return err
			}
			if cfg.Remote == "" {
				return nil // nothing to pull
			}
			staging, err := config.StagingDir()
			if err != nil {
				return err
			}

			if _, err := os.Stat(filepath.Join(staging, ".git")); err != nil {
				if _, err := gitrepo.Clone(ctx, cfg.Remote, staging); err != nil {
					fmt.Fprintf(out, "clauderig pull: clone skipped: %v\n", err)
				}
				return nil // best-effort: never block SessionStart
			}
			repo, err := gitrepo.Open(ctx, staging)
			if err != nil {
				return nil
			}
			if err := repo.Pull(ctx, "origin", "main"); err != nil {
				fmt.Fprintf(out, "clauderig pull: %v\n", err)
			}
			return nil
		},
	}
}
