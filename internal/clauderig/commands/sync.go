package commands

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/core/pathmap"
	"github.com/rigsmith/rigsmith/internal/clauderig/account"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/devices"
	"github.com/rigsmith/rigsmith/internal/clauderig/engine"
	"github.com/rigsmith/rigsmith/internal/clauderig/redact"
	"github.com/spf13/cobra"
)

// configHistoryMaxCommits bounds the config-history side branch: once it grows
// past this, it's squashed to a single commit (it's tiny, so this is generous).
const configHistoryMaxCommits = 200

// pushAttempts bounds the push/reconcile retry loop. Small on purpose: each round
// is a real fetch+merge, and if the remote is moving faster than that the next
// scheduled sync is the right place to catch up, not a loop here.
const pushAttempts = 3

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
			// Settle any merge an earlier run abandoned before the snapshot writes
			// into the staging tree — committing over a conflicted index would
			// publish the conflict markers themselves. If it cannot be settled,
			// STOP: this path stages and commits, so carrying on is the hazard.
			if !repairWedgedMerge(ctx, out, staging, true) {
				return fmt.Errorf("the staging repo is still mid-merge — resolve it in %s, or run `clauderig doctor --fix`", staging)
			}
			claudeVer := ""
			if cliLoc, st := cfg.RootLocation("cli", me); st == pathmap.StatusResolved {
				claudeVer = config.DetectClaudeVersion(cliLoc)
			}
			// Attribution for ledger rows no Desktop sidecar covers. Read once,
			// before the walk, so every row this sync records is stamped with the
			// same account rather than one that could change mid-run.
			// Read ONCE, and reuse for both writes below. Reading again for the
			// device registry would let a login change (or one transiently
			// failing read) mid-sync stamp ledger rows with one uuid and the
			// registry with another — and the registry is what resolves an
			// alias or email back to that uuid, so the two disagreeing breaks
			// `search --account` for exactly those rows.
			liveAcct, liveOrg, liveEmail, _ := account.LiveIdentity()
			rep, serr := engine.Sync(engine.Options{
				StagingDir: staging, Config: cfg, Machine: me, ClaudeVersion: claudeVer,
				RetentionDays:   cfg.Retention.HistoryDays,
				MaxFileBytes:    cfg.Retention.MaxFileBytes,
				Profiles:        engine.LocalProfileNames(),
				LiveAccountUUID: liveAcct,
			})
			if rep != nil {
				w := 0
				for _, r := range rep.Roots {
					w = rootColumn(w, r.ID)
				}
				for _, r := range rep.Roots {
					if r.Skipped {
						fmt.Fprintf(out, "  %-*s %s\n", w, r.ID, DimStyle.Render("skipped (absent here)"))
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
					if r.Disallowed > 0 {
						extra += fmt.Sprintf(", %d no longer allowed", r.Disallowed)
					}
					if r.Shadowed > 0 {
						extra += fmt.Sprintf(", %d stale placeholder(s) retired", r.Shadowed)
					}
					if n := len(r.Oversize); n > 0 {
						extra += fmt.Sprintf(", %d too large", n)
					}
					fmt.Fprintf(out, "  %-*s %d files, %d secret field(s) redacted%s\n", w, r.ID, r.Files, r.Redactions, extra)
					// Name what was dropped for size — a silent cap reads as "everything
					// synced" when it didn't, and these are whole conversations.
					for _, rel := range r.Oversize {
						fmt.Fprintf(out, "    %s %s\n", DimStyle.Render("too large:"), DimStyle.Render(rel))
					}
				}
				fmt.Fprintf(out, "  manifest  %d projects\n", rep.ManifestProjects)
				if rep.LedgerTotal > 0 {
					fmt.Fprintf(out, "  ledger    %d session(s) remembered (+%d)\n", rep.LedgerTotal, rep.LedgerAdded)
				}
				if rep.LedgerError != "" {
					fmt.Fprintf(out, "%s\n", WarnStyle.Render("  ledger    not updated: "+rep.LedgerError))
				}
				if rep.RetentionPruned > 0 {
					fmt.Fprintf(out, "  retention %d aged file(s) pruned from staging\n", rep.RetentionPruned)
				}
				if rep.SidecarsPruned > 0 {
					fmt.Fprintf(out, "  sidecars  %d orphaned session(s) pruned from staging\n", rep.SidecarsPruned)
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

			// Record this machine in the synced device registry, together with the
			// account it synced as — identity only (see devices.Account), and the
			// only account provenance anything in the repo carries. Best-effort:
			// an unreadable identity leaves the previous record standing and never
			// costs anyone a sync.
			if reg, err := devices.Load(staging); err == nil {
				var acct *devices.Account
				// Both halves required, from the ONE read above. An `||` gate
				// built a non-nil record from any single field, so a partial
				// read replaced a complete Device.Account with a fragment — and
				// Touch keeps a nil precisely to preserve the previous value.
				// organizationUuid may legitimately be absent; the uuid and the
				// email are what make a record usable, since one is the join key
				// and the other is what a person types.
				if liveAcct != "" && liveEmail != "" {
					// The registry is written AFTER engine.Sync finishes its scan
					// and is committed directly, so these values never pass the
					// tripwire that guards every other synced file. The argument
					// that a uuid and an email cannot trip it is about their
					// SHAPE, and nothing guarantees the shape.
					candidate := &devices.Account{AccountUUID: liveAcct, OrganizationUUID: liveOrg, Email: liveEmail}
					if f := scanIdentity(candidate); f != nil {
						fmt.Fprintf(out, "%s\n", WarnStyle.Render(fmt.Sprintf(
							"⚠ account identity not recorded: %s looks like %s — check what ~/.claude.json holds", f.Path, f.Kind)))
					} else {
						acct = candidate
					}
				}
				reg.Touch(me.Name, me.OS, claudeVer, acct, time.Now())
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
			// recovers; an in-sync push is a cheap no-op. A rejection means the
			// remote advanced, so reconcile and try again — and keep trying a few
			// times, because with several machines syncing on a timer another one
			// can land a push while this one is still merging. Failing there would
			// report a broken sync for a race that resolves itself on the retry.
			for attempt := 0; ; attempt++ {
				perr := repo.Push(ctx, "origin", "main")
				if perr == nil {
					break
				}
				if attempt >= pushAttempts {
					return fmt.Errorf("push after reconcile: %w", perr)
				}
				if err := reconcile(ctx, out, repo, "origin", "main", true); err != nil {
					return err
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
	cmd.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "stage and scan, but don't commit or push")
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

// scanIdentity runs the same content rules over the three identity values that
// guard every other synced file, returning the first finding or nil.
//
// It exists because the device registry is written after engine.Sync's scan and
// committed directly, so nothing else would ever look at these. Each field is
// scanned under its own name so the warning can say which one is wrong.
func scanIdentity(a *devices.Account) *redact.Finding {
	for _, f := range []struct{ name, value string }{
		{"accountUuid", a.AccountUUID},
		{"organizationUuid", a.OrganizationUUID},
		{"email", a.Email},
	} {
		if f.value == "" {
			continue
		}
		// Rejected outright, before the content rules see it. ScanFile skips its
		// entropy check for multiline values — reasonable for a file, wrong
		// here — so a newline in an identity field would carry whatever follows
		// it straight past the scan and into the pushed registry. No real uuid
		// or email contains one.
		if strings.ContainsAny(f.value, "\r\n") {
			return &redact.Finding{Path: f.name, Kind: "multiline identity"}
		}
		if found := redact.ScanFile(f.name, []byte(f.value)); len(found) > 0 {
			hit := found[0]
			hit.Path = f.name
			return &hit
		}
	}
	return nil
}
