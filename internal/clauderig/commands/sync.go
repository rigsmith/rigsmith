package commands

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mattn/go-isatty"
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

// hookTranscripts reads the Claude Code hook payload from stdin, if one is
// there, and returns the transcript it names. Every hook event carries
// `transcript_path`; SessionEnd is the one that runs `sync --flush`. Nothing
// is read from a terminal — a person running the command has no payload to
// give — and an unreadable or payload-less stream (a pipe from /dev/null)
// means "none named", which the caller treats as a flush of everything.
func hookTranscripts(in io.Reader) []string {
	if f, ok := in.(*os.File); ok && isatty.IsTerminal(f.Fd()) {
		return nil
	}
	// Bounded, and not waited on for long: a stream that never closes must
	// not hold the sync hostage, and a real payload is a few hundred bytes.
	type result struct {
		body []byte
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		b, err := io.ReadAll(io.LimitReader(in, 1<<20))
		ch <- result{b, err}
	}()
	var body []byte
	select {
	case r := <-ch:
		if r.err != nil {
			return nil
		}
		body = r.body
	case <-time.After(2 * time.Second):
		return nil
	}
	var payload struct {
		TranscriptPath string `json:"transcript_path"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || strings.TrimSpace(payload.TranscriptPath) == "" {
		return nil
	}
	return []string{payload.TranscriptPath}
}

// pushAttempts bounds the push/reconcile retry loop. Small on purpose: each round
// is a real fetch+merge, and if the remote is moving faster than that the next
// scheduled sync is the right place to catch up, not a loop here.
const pushAttempts = 3

// NewSyncCmd builds the `sync` command — walk → redact → manifest → tripwire into
// the staging repo, then commit (empty-guarded) and push. Streams the report so
// redaction is visible, not magic. The tripwire fails the sync loudly if a secret
// slips past redaction; nothing is pushed in that case.
func NewSyncCmd() *cobra.Command {
	var dryRun, flush bool
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Snapshot, redact, rewrite, and push your Claude Code setup",
		Long: "Walks the sync roots, redacts secret-bearing fields, rewrites machine\n" +
			"paths into a portable form, commits, and pushes.\n\n" +
			"A transcript over retention.largeFileBytes is restaged only once it has\n" +
			"grown by half that again, or once it has gone quiet for 30 minutes, so the\n" +
			"Stop hook does not re-commit a 50 MB file every turn. The SessionEnd hook\n" +
			"runs `sync --flush`, which restages the ended session's transcript (the\n" +
			"hook names it on stdin) regardless, so its last turn never waits for the\n" +
			"next session; other sessions' transcripts keep their throttle. Run by\n" +
			"hand, `--flush` restages every changed transcript.",
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
			// Validated HERE, before anything consumes it. There are two ways out
			// of this variable — the ledger, via engine.Sync, and the device
			// registry below — and only the second was checked, so an identity
			// that looks like a secret was staged into ledger rows and pushed
			// while the registry record that would have carried it was
			// suppressed. Clearing all three keeps the two paths from
			// disagreeing about what is safe to record.
			// Canonicalised at the SOURCE. A whitespace-padded or non-uuid value
			// was persisted verbatim as a sticky attribution, while the filter
			// canonicalises its input — so the session stopped matching its own
			// account, and a later correct attribution could not replace it
			// because it carries the same rank.
			liveAcct = account.CanonicalUUID(liveAcct)
			if f := scanIdentity(&devices.Account{AccountUUID: liveAcct, OrganizationUUID: liveOrg, Email: liveEmail}); f != nil {
				fmt.Fprintf(out, "%s\n", WarnStyle.Render(fmt.Sprintf(
					"⚠ account identity not recorded: %s looks like %s — check what ~/.claude.json holds", f.Path, f.Kind)))
				liveAcct, liveOrg, liveEmail = "", "", ""
			}
			// --flush: the session is over and its tail has nothing to wait
			// for. From the SessionEnd hook that is one transcript — the hook
			// says which — and only that one skips the throttle; run by hand,
			// with nothing on stdin to say, every transcript does.
			largeFileBytes := cfg.Retention.LargeFileBytes
			var flushPaths []string
			if flush {
				if flushPaths = hookTranscripts(cmd.InOrStdin()); len(flushPaths) == 0 {
					largeFileBytes = -1
				}
			}
			rep, serr := engine.Sync(engine.Options{
				StagingDir: staging, Config: cfg, Machine: me, ClaudeVersion: claudeVer,
				RetentionDays:   cfg.Retention.HistoryDays,
				MaxFileBytes:    cfg.Retention.MaxFileBytes,
				LargeFileBytes:  largeFileBytes,
				Flush:           flushPaths,
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
					if n := len(r.Oversize); n > 0 {
						extra += fmt.Sprintf(", %d too large", n)
					}
					if r.Deferred > 0 {
						extra += fmt.Sprintf(", %d large transcript(s) waiting for more content or to settle", r.Deferred)
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
				// Already scanned above, and cleared if it failed — so reaching
				// here with both halves present means it is safe to record.
				if liveAcct != "" && liveEmail != "" {
					acct = &devices.Account{AccountUUID: liveAcct, OrganizationUUID: liveOrg, Email: liveEmail}
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
	cmd.Flags().BoolVar(&flush, "flush", false, "restage the ended session's transcript (from the hook payload on stdin), or every changed transcript when run by hand, past the large-file throttle")
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

// emailShape is a deliberately conservative check: one @, no spaces or control
// characters, a dot in the domain. It is not RFC-complete and does not need to
// be — the question here is "could this be something other than an email", and
// anything unusual is worth refusing to publish.
var emailShape = regexp.MustCompile(`^[^\s@\x00-\x1f]{1,128}@[^\s@\x00-\x1f]{1,128}\.[^\s@.\x00-\x1f]{2,63}$`)

// scanIdentity validates the three identity values by SHAPE, returning the
// first that does not fit.
//
// Positive validation, not scanning, and that inversion is the point. Three
// separate findings arrived for three different ways a value slipped past
// redact.ScanFile — multiline values skip its entropy check, oversized ones
// exceed its content cap and return nothing at all, and binary bytes trip its
// binary guard, which returns clean BEFORE secret detection runs. Each was
// patched in turn and a fourth was always available, because asking "does this
// look like a secret" has an open-ended set of ways to answer no.
//
// A uuid and an email have exact shapes. Requiring them ends the class: a value
// that is not one of those two things is refused whatever it happens to be.
func scanIdentity(a *devices.Account) *redact.Finding {
	if a.AccountUUID != "" && account.CanonicalUUID(a.AccountUUID) == "" {
		return &redact.Finding{Path: "accountUuid", Kind: "not a uuid"}
	}
	if a.OrganizationUUID != "" && account.CanonicalUUID(a.OrganizationUUID) == "" {
		return &redact.Finding{Path: "organizationUuid", Kind: "not a uuid"}
	}
	if a.Email != "" && !emailShape.MatchString(a.Email) {
		return &redact.Finding{Path: "email", Kind: "not an email"}
	}
	// Shape-valid values still go through the content rules, so a uuid-shaped
	// string that somehow reads as a known token prefix is still caught.
	for _, f := range []struct{ name, value string }{
		{"accountUuid", a.AccountUUID},
		{"organizationUuid", a.OrganizationUUID},
		{"email", a.Email},
	} {
		if f.value == "" {
			continue
		}
		if found := redact.ScanFile(f.name, []byte(f.value)); len(found) > 0 {
			hit := found[0]
			hit.Path = f.name
			return &hit
		}
	}
	return nil
}
