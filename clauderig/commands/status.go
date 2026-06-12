package commands

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/rigsmith/clauderig/internal/allowlist"
	"github.com/rigsmith/clauderig/internal/config"
	"github.com/rigsmith/clauderig/internal/gitrepo"
	"github.com/rigsmith/clauderig/internal/hooks"
	"github.com/rigsmith/core/pathmap"
	"github.com/spf13/cobra"
)

// NewStatusCmd builds the `status` command — a read-only summary of sync state:
// machine, remote reachability, last sync, per-root file counts, and hooks. Plain
// styled output (scriptable); the live view lives in `ui`.
func NewStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show sync state: remote, last sync, roots, hooks",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			out := cmd.OutOrStdout()

			cfg, err := config.LoadOrDefault()
			if err != nil {
				return err
			}
			me := config.Detect(machineName(cfg))
			staging, _ := config.StagingDir()

			fmt.Fprintln(out, HeaderStyle.Render("clauderig status"))
			fmt.Fprintf(out, "  machine   %s (%s)\n", me.Name, me.OS)

			// Remote
			if cfg.Remote == "" {
				fmt.Fprintf(out, "  remote    %s\n", DimStyle.Render("none configured — run `clauderig init`"))
			} else {
				rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
				reach := gitrepo.Reachable(rctx, cfg.Remote)
				cancel()
				mark := ErrStyle.Render("unreachable")
				if reach {
					mark = OkStyle.Render("reachable")
				}
				fmt.Fprintf(out, "  remote    %s  %s\n", cfg.Remote, mark)
			}

			// Staging repo / last sync
			if _, err := os.Stat(filepath.Join(staging, ".git")); err == nil {
				if repo, err := gitrepo.Open(ctx, staging); err == nil {
					if h, subj, when, err := repo.LastCommit(ctx); err == nil {
						fmt.Fprintf(out, "  last sync %s %s %s\n", h, when, DimStyle.Render(subj))
					}
					if dirty, _ := repo.Dirty(ctx); dirty {
						fmt.Fprintf(out, "            %s\n", WarnStyle.Render("staging has uncommitted changes"))
					}
				}
			} else {
				fmt.Fprintf(out, "  last sync %s\n", DimStyle.Render("never (no staging repo yet)"))
			}

			// Roots
			fmt.Fprintln(out, DimStyle.Render("  roots:"))
			for _, r := range cfg.Roots {
				if !r.Enabled {
					continue
				}
				loc, st := cfg.RootLocation(r.ID, me)
				if st != pathmap.StatusResolved || !dirExists(loc) {
					fmt.Fprintf(out, "  %-8s %s\n", r.ID, DimStyle.Render("absent here"))
					continue
				}
				files, _ := allowlist.Walk(loc, allowlistFor(r.ID))
				fmt.Fprintf(out, "  %-8s %d files\n", r.ID, len(files))
			}

			// Hooks
			if path, err := settingsPath(); err == nil {
				if present, _ := hooks.Status(path); len(present) > 0 {
					fmt.Fprintf(out, "  hooks     %v\n", present)
				} else {
					fmt.Fprintf(out, "  hooks     %s\n", DimStyle.Render("not installed (run `clauderig hooks install`)"))
				}
			}
			return nil
		},
	}
}

func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

func allowlistFor(rootID string) allowlist.List {
	if rootID == "desktop" {
		return allowlist.Desktop()
	}
	return allowlist.CLI()
}
