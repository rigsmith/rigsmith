package commands

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/rigsmith/clauderig/internal/config"
	"github.com/rigsmith/clauderig/internal/engine"
	"github.com/rigsmith/clauderig/internal/gitrepo"
	"github.com/rigsmith/clauderig/internal/manifest"
	"github.com/rigsmith/core/pathmap"
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
			staging, err := config.StagingDir()
			if err != nil {
				return err
			}

			// Update the staging repo from the remote (best-effort; never blocks).
			if cfg.Remote != "" {
				if _, err := os.Stat(filepath.Join(staging, ".git")); err != nil {
					if _, err := gitrepo.Clone(ctx, cfg.Remote, staging); err != nil {
						fmt.Fprintf(out, "clauderig pull: clone skipped: %v\n", err)
					}
				} else if repo, err := gitrepo.Open(ctx, staging); err == nil {
					if err := repo.Pull(ctx, "origin", "main"); err != nil {
						fmt.Fprintf(out, "clauderig pull: %v\n", err)
					}
				}
			}

			autoRestoreIfFresh(ctx, out, cfg, staging)
			return nil
		},
	}
}

// autoRestoreIfFresh restores onto this machine when AutoRestore is set AND the
// machine is fresh (no projects yet) — so a new computer wires itself up on first
// session without ever clobbering an established one. Best-effort and silent on
// failure (it runs from the SessionStart hook).
func autoRestoreIfFresh(ctx context.Context, out io.Writer, cfg *config.Config, staging string) {
	if !cfg.AutoRestore {
		return
	}
	me := config.Detect(machineName(cfg))
	cliLoc, st := cfg.RootLocation("cli", me)
	if st != pathmap.StatusResolved {
		return
	}
	if entries, err := os.ReadDir(filepath.Join(cliLoc, "projects")); err == nil && len(entries) > 0 {
		return // not fresh — never auto-restore over an established machine
	}
	man, err := manifest.Load(staging)
	if err != nil {
		return
	}
	if _, err := engine.Restore(engine.RestoreOptions{
		StagingDir: staging, Config: cfg, Machine: me, Manifest: man, Prune: cfg.AlwaysPrune,
	}); err == nil {
		fmt.Fprintln(out, "clauderig: fresh machine — auto-restored from sync")
	}
}
