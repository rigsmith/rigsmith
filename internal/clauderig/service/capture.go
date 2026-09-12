package service

import (
	"context"
	"fmt"

	"github.com/rigsmith/rigsmith/core/commandrun"
	"github.com/rigsmith/rigsmith/core/pathmap"
	"github.com/rigsmith/rigsmith/internal/agentrig/storelock"
	"github.com/rigsmith/rigsmith/internal/clauderig/account"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/devices"
	"github.com/rigsmith/rigsmith/internal/clauderig/engine"
	"github.com/rigsmith/rigsmith/internal/clauderig/journal"
	"github.com/rigsmith/rigsmith/internal/clauderig/transcript"
)

// Capture repairs the store, captures and scans sources, and records metadata.
// It does not commit the new capture or publish it; merge repair may finish a
// previously interrupted merge commit. Its output is a mutable staging checkout,
// not a durable queue artifact. Composing callers must hold the store lease
// through capture and sealing/publication; Capture borrows that lease.
// SyncRequest retains the same dry-run, identity and flush semantics as Sync.
func (s Service) Capture(ctx context.Context, req SyncRequest) (*engine.Report, error) {
	return s.capture(ctx, req, nil)
}

// captureInputs is used only for sealed queued captures. Nil preserves live
// discovery and source resolution for synchronous callers.
type captureInputs struct {
	sources             map[string]string
	profiles            []string
	attributionSessions map[string]bool
}

func (s Service) capture(ctx context.Context, req SyncRequest, inputs *captureInputs) (report *engine.Report, rerr error) {
	if inputs == nil {
		if err := requireCanonicalRunner(ctx, req.AllowMergeTool); err != nil {
			return nil, err
		}
	}
	if req.Config == nil {
		return nil, fmt.Errorf("capture requires a configuration")
	}
	ctx, release, err := storelock.Acquire(ctx, req.StagingDir, StoreWait)
	if err != nil {
		return nil, err
	}
	defer release()
	if inputs == nil {
		ctx = canonicalContext(ctx)
	}
	defer func() { rerr = canonicalResult(ctx, rerr) }()
	cfg, me, staging, dryRun := req.Config, req.Machine, req.StagingDir, req.DryRun

	s.emit(SyncStarted{})
	// Settle any merge an earlier run abandoned before the snapshot writes
	// into the staging tree — committing over a conflicted index would
	// publish the conflict markers themselves. If it cannot be settled,
	// STOP: this path stages and commits, so carrying on is the hazard.
	if inputs == nil && !s.RepairMerge(ctx, staging, req.AllowMergeTool).Safe {
		if err := commandrun.Check(ctx); err != nil {
			return report, err
		}
		return report, fmt.Errorf("the staging repo is still mid-merge — resolve it in %s, or run `clauderig doctor --fix`", staging)
	}
	if err := commandrun.Check(ctx); err != nil {
		return report, err
	}
	claudeVer := ""
	if cliLoc, st := cfg.RootLocation("cli", me); st == pathmap.StatusResolved {
		if inputs != nil {
			cliLoc = inputs.sources["cli"]
		}
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
	identity, _ := s.liveIdentity()
	liveAcct, liveOrg, liveEmail := identity.AccountUUID, identity.OrganizationUUID, identity.Email
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
		s.emit(IdentityRejected{Finding: *f})
		liveAcct, liveOrg, liveEmail = "", "", ""
	}
	// Resolve command input after repair and identity capture, exactly where
	// the CLI used to read it. Programmatic callers pass an already decoded intent.
	flush := req.Flush
	if req.ResolveFlush != nil {
		flush = req.ResolveFlush()
	}
	largeFileBytes := cfg.Retention.LargeFileBytes
	var flushPaths []string
	switch flush.Mode {
	case FlushNormal:
	case FlushSelected:
		flushPaths = flush.Paths
	case FlushAll:
		largeFileBytes = -1
	default:
		return report, fmt.Errorf("invalid flush mode %d", flush.Mode)
	}
	chunked, cerr := transcript.Enabled(staging)
	if cerr != nil {
		return report, cerr
	}
	if cfg.ChunkTranscripts != nil {
		chunked = *cfg.ChunkTranscripts
	}
	var profiles []string
	var sources map[string]string
	var attributionSessions map[string]bool
	if inputs == nil {
		profiles = engine.LocalProfileNames()
	} else {
		profiles, sources = inputs.profiles, inputs.sources
		attributionSessions = inputs.attributionSessions
	}
	if req.checkCapture != nil {
		if err := req.checkCapture(profiles); err != nil {
			return report, err
		}
	}
	rep, serr := engine.Sync(engine.Options{
		ChunkTranscripts: chunked,
		StagingDir:       staging, Config: cfg, Machine: me, ClaudeVersion: claudeVer,
		RetentionDays:       cfg.Retention.HistoryDays,
		MaxFileBytes:        cfg.Retention.MaxFileBytes,
		LargeFileBytes:      largeFileBytes,
		Flush:               flushPaths,
		RedactTranscripts:   cfg.RedactTranscripts,
		Profiles:            profiles,
		SourceOverride:      sources,
		LiveAccountUUID:     liveAcct,
		AttributionSessions: attributionSessions,
	})
	report = rep
	if rep != nil {
		s.emit(Captured{Report: rep})
	}
	// Journal what the engine did *before* committing, so the record
	// travels in this sync's own commit. Written afterwards it would
	// leave the tree dirty until the next run and make `status` report
	// uncommitted changes forever. A dry run is deliberately not
	// recorded — it's a preview, and a feed that claims previews
	// happened to your data is worse than no feed.
	if !dryRun {
		_ = journal.Append(staging, journal.FromSync(me.Name, rep, serr))
	}

	if serr != nil {
		if rep != nil {
			s.emit(CaptureFailed{Findings: rep.Findings, Err: serr})
		}
		return report, serr
	}
	if dryRun {
		s.emit(DryRunStaged{})
		return report, nil
	}

	// A delayed capture must not stamp the current device registry with an old
	// event's source account. Retain the registry copied from canonical staging.
	if inputs != nil {
		return report, nil
	}

	// Record this machine in the synced device registry, together with the
	// account it synced as — identity only (see devices.Account), and the
	// only account provenance anything in the repo carries. Best-effort:
	// an unreadable identity leaves the previous record standing and never
	// costs anyone a sync.
	//
	// Gated on a resolved machine name: registering the placeholder is what
	// put a ghost device named "this" into the registry for two months; it
	// syncs, so every other machine inherits the confusion.
	if !config.IdentityResolved(cfg) {
		s.emit(DeviceUnregistered{})
	} else if reg, err := devices.Load(staging); err == nil {
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
		reg.Touch(me.Name, me.OS, claudeVer, acct, s.now())
		_ = reg.Save(staging)
	}

	return report, nil
}
