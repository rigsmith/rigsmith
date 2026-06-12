package commands

import (
	"fmt"

	"github.com/rigsmith/clauderig/internal/config"
	"github.com/rigsmith/clauderig/internal/engine"
	"github.com/rigsmith/clauderig/internal/gitrepo"
	"github.com/spf13/cobra"
)

// NewSyncCmd builds the `sync` command — walk → redact → manifest → tripwire into
// the staging repo, then commit (empty-guarded) and push. Streams the report so
// redaction is visible, not magic. The tripwire fails the sync loudly if a secret
// slips past redaction; nothing is pushed in that case.
func NewSyncCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Snapshot, redact, rewrite, and push your Claude Code setup",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			out := cmd.OutOrStdout()

			cfg, err := config.LoadOrDefault()
			if err != nil {
				return err
			}
			me := config.Detect(machineName(cfg))
			staging, err := config.StagingDir()
			if err != nil {
				return err
			}

			fmt.Fprintln(out, HeaderStyle.Render("clauderig sync"))
			rep, serr := engine.Sync(engine.Options{
				StagingDir: staging, Config: cfg, Machine: me,
			})
			if rep != nil {
				for _, r := range rep.Roots {
					if r.Skipped {
						fmt.Fprintf(out, "  %-8s %s\n", r.ID, DimStyle.Render("skipped (absent here)"))
						continue
					}
					fmt.Fprintf(out, "  %-8s %d files, %d secret field(s) redacted\n", r.ID, r.Files, r.Redactions)
				}
				fmt.Fprintf(out, "  manifest  %d projects\n", rep.ManifestProjects)
			}
			if serr != nil {
				for _, f := range rep.Findings {
					fmt.Fprintf(out, "  %s %s (%s)\n", ErrStyle.Render("LEAK"), f.Path, f.Kind)
				}
				return serr
			}
			if dryRun {
				fmt.Fprintln(out, DimStyle.Render("\n  dry-run: staged + scanned, not committing"))
				return nil
			}

			repo, err := gitrepo.Init(ctx, staging)
			if err != nil {
				return err
			}
			if cfg.Remote != "" {
				if err := repo.SetRemote(ctx, "origin", cfg.Remote); err != nil {
					return err
				}
			}
			changed, err := repo.Commit(ctx, "clauderig sync: "+me.Name)
			if err != nil {
				return err
			}
			if !changed {
				fmt.Fprintln(out, OkStyle.Render("\n  ✓ already up to date"))
				return nil
			}
			if cfg.Remote == "" {
				fmt.Fprintln(out, OkStyle.Render("\n  ✓ committed locally (no remote configured — run init)"))
				return nil
			}
			if err := repo.Push(ctx, "origin", "main"); err != nil {
				return fmt.Errorf("push: %w", err)
			}
			fmt.Fprintln(out, OkStyle.Render("\n  ✓ synced & pushed"))
			return nil
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "stage and scan, but don't commit or push")
	return cmd
}

// machineName returns this machine's configured name, or "this" if not yet named.
func machineName(cfg *config.Config) string {
	for name := range cfg.Machines {
		return name
	}
	return "this"
}
