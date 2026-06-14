package commands

import (
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/core/pathmap"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/devices"
	"github.com/rigsmith/rigsmith/internal/clauderig/engine"
	"github.com/spf13/cobra"
)

// configHistoryMaxCommits bounds the config-history side branch: once it grows
// past this, it's squashed to a single commit (it's tiny, so this is generous).
const configHistoryMaxCommits = 200

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
			claudeVer := ""
			if cliLoc, st := cfg.RootLocation("cli", me); st == pathmap.StatusResolved {
				claudeVer = config.DetectClaudeVersion(cliLoc)
			}
			rep, serr := engine.Sync(engine.Options{
				StagingDir: staging, Config: cfg, Machine: me, ClaudeVersion: claudeVer,
				RetentionDays: cfg.Retention.HistoryDays,
			})
			if rep != nil {
				for _, r := range rep.Roots {
					if r.Skipped {
						fmt.Fprintf(out, "  %-8s %s\n", r.ID, DimStyle.Render("skipped (absent here)"))
						continue
					}
					extra := ""
					if r.Unchanged > 0 {
						extra += fmt.Sprintf(", %d unchanged", r.Unchanged)
					}
					if r.RetentionByAge > 0 {
						extra += fmt.Sprintf(", %d aged out", r.RetentionByAge)
					}
					if r.SkippedFiles > 0 {
						extra += fmt.Sprintf(", %d skipped (churn)", r.SkippedFiles)
					}
					fmt.Fprintf(out, "  %-8s %d files, %d secret field(s) redacted%s\n", r.ID, r.Files, r.Redactions, extra)
				}
				fmt.Fprintf(out, "  manifest  %d projects\n", rep.ManifestProjects)
				if rep.RetentionPruned > 0 {
					fmt.Fprintf(out, "  retention %d aged file(s) pruned from staging\n", rep.RetentionPruned)
				}
			}
			if serr != nil {
				if rep != nil {
					for _, f := range rep.Findings {
						fmt.Fprintf(out, "  %s %s (%s)\n", ErrStyle.Render("LEAK"), f.Path, f.Kind)
					}
				}
				return serr
			}
			if dryRun {
				fmt.Fprintln(out, DimStyle.Render("\n  dry-run: staged + scanned, not committing"))
				return nil
			}

			// Record this machine in the synced device registry.
			if reg, err := devices.Load(staging); err == nil {
				reg.Touch(me.Name, me.OS, claudeVer, time.Now())
				_ = reg.Save(staging)
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
			if cfg.Remote == "" {
				if changed {
					fmt.Fprintln(out, OkStyle.Render("\n  ✓ committed locally (no remote — run init)"))
				} else {
					fmt.Fprintln(out, OkStyle.Render("\n  ✓ already up to date (no remote)"))
				}
				return nil
			}
			// Always push (even with no new commit) so a previously-failed push
			// recovers; an in-sync push is a cheap no-op.
			if err := repo.Push(ctx, "origin", "main"); err != nil {
				// The remote likely advanced (another machine pushed). Reconcile:
				// fetch + merge, hand conflicts to git mergetool, then push again.
				conflicted, merr := repo.FetchMerge(ctx, "origin", "main")
				if merr != nil {
					return fmt.Errorf("push rejected and reconcile failed: %w", merr)
				}
				if conflicted {
					if !interactive() {
						_ = repo.AbortMerge(ctx)
						return fmt.Errorf("remote diverged with conflicts; re-run `clauderig sync` in a terminal to resolve via git mergetool")
					}
					fmt.Fprintln(out, WarnStyle.Render("  merge conflicts — launching git mergetool…"))
					if err := repo.RunMergeTool(ctx); err != nil {
						_ = repo.AbortMerge(ctx)
						return fmt.Errorf("mergetool: %w", err)
					}
					if err := repo.CommitMerge(ctx); err != nil {
						return err
					}
				}
				if err := repo.Push(ctx, "origin", "main"); err != nil {
					return fmt.Errorf("push after reconcile: %w", err)
				}
			}
			if changed {
				fmt.Fprintln(out, OkStyle.Render("\n  ✓ synced & pushed"))
			} else {
				fmt.Fprintln(out, OkStyle.Render("\n  ✓ in sync"))
			}

			// Preserve config history on a separate branch that survives main's
			// squash (everything except the disposable transcript tree). Bounded:
			// squash it once its commit count grows large. Best-effort throughout.
			if changed, cerr := repo.CommitSubtree(ctx, "config-history", []string{".", ":!cli/projects"}, "clauderig config: "+me.Name); cerr == nil && changed {
				if repo.BranchCommitCount(ctx, "config-history") > configHistoryMaxCommits {
					if err := repo.SquashBranch(ctx, "config-history", "clauderig: squashed config history"); err == nil && cfg.Remote != "" {
						_ = repo.ForcePushBranch(ctx, "origin", "config-history")
					}
				} else if cfg.Remote != "" {
					_ = repo.PushBranch(ctx, "origin", "config-history")
				}
			}

			// Size-based squash: bound .git when transcript history has bloated it.
			gitBytes, _ := repo.GitDirBytes(ctx)
			wtBytes, _ := repo.WorkTreeBytes(ctx)
			if gitrepo.ShouldSquash(gitBytes, wtBytes, cfg.Retention.FloorBytes, cfg.Retention.SquashFactor) {
				fmt.Fprintf(out, "  %s history squash (.git %dMB > %.0f× worktree)\n",
					DimStyle.Render("⟳"), gitBytes>>20, cfg.Retention.SquashFactor)
				if err := repo.Squash(ctx, "clauderig: squashed history"); err != nil {
					return fmt.Errorf("squash: %w", err)
				}
				if err := repo.ForcePush(ctx, "origin", "main"); err != nil {
					return fmt.Errorf("force-push after squash: %w", err)
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "stage and scan, but don't commit or push")
	return cmd
}

// machineName returns this host's configured machine name. It identifies the
// local machine by its stable path identity (OS token + home directory) rather
// than picking an arbitrary map entry, so a config that registers more than one
// machine resolves deterministically to the right one instead of flipping with
// Go's randomized map iteration. Falls back to the OS hostname, then "this",
// when no registered machine matches this host.
func machineName(cfg *config.Config) string {
	localOS := config.OSToken()
	home, _ := os.UserHomeDir()

	names := make([]string, 0, len(cfg.Machines))
	for name := range cfg.Machines {
		names = append(names, name)
	}
	sort.Strings(names) // deterministic order if several entries somehow match
	for _, name := range names {
		if m := cfg.Machines[name]; m.OS == localOS && m.Home == home {
			return name
		}
	}
	if host, err := os.Hostname(); err == nil && host != "" {
		return host
	}
	return "this"
}
