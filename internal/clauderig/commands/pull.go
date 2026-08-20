package commands

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/core/pathmap"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/engine"
	"github.com/rigsmith/rigsmith/internal/clauderig/manifest"
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
					// An unfinished merge makes the ff-only pull below fail on every
					// future session, so clear it first rather than reporting the
					// same error forever.
					repairWedgedMerge(ctx, out, staging, false)
					if err := repo.Pull(ctx, "origin", "main"); err != nil {
						// A non-ff divergence is not an error to report and forget —
						// it never resolves itself. Merge it here (policies decide,
						// no prompt) so the next sync has one line of history to push.
						// Report the RECONCILE failure, not the ff-only one that sent us
						// here: the ff error is a symptom of divergence, while this one
						// names the path that needs a human and how to finish it — which
						// is the only message that ends the wedge.
						if rerr := reconcile(ctx, out, repo, "origin", "main", false); rerr != nil {
							fmt.Fprintf(out, "clauderig pull: %v\n", rerr)
						}
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
	if rep, err := engine.Restore(engine.RestoreOptions{
		StagingDir: staging, Config: cfg, Machine: me, Manifest: man, Prune: cfg.AlwaysPrune,
		Profiles: engine.StagedProfileNames(staging),
	}); err == nil {
		fmt.Fprintln(out, "clauderig: fresh machine — auto-restored from sync")
		if n := rep.DesktopSessions(); n > 0 {
			printDesktopRestartNudge(out, n)
		}
	}
}
