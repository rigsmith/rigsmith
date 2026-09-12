// Package engine is the sync pipeline: walk each root's allowlist, redact
// secrets, rewrite machine paths into portable templates, copy into the staging
// repo, write the manifest, and run the tripwire over everything about to be
// published. It is pure of git — the caller commits and pushes the staging
// directory.
//
// One rule shapes the whole thing and is worth stating before any of the code:
// ROLLOUT BYTES ARE NEVER EDITED on the way out, beyond an optional and explicit
// credential scrub. clauderig rewrites Claude Code's project SLUGS because a
// slug is a directory NAME derived from a path, and a name can be translated
// without touching content. Codex records the working directory INSIDE the
// rollout, so the equivalent would be rewriting the middle of a conversation to
// make a path look local — editing the artifact in order to back it up. codexrig
// does not do that. A rollout restores byte-for-byte, `codex resume --cd` exists
// for the case where the directory moved, and the manifest carries a portable
// spelling of each directory so a listing can still show something meaningful.
package engine

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rigsmith/rigsmith/core/pathmap"
	"github.com/rigsmith/rigsmith/internal/agentrig/allowlist"
	"github.com/rigsmith/rigsmith/internal/agentrig/redact"
	cxallow "github.com/rigsmith/rigsmith/internal/codexrig/allowlist"
	"github.com/rigsmith/rigsmith/internal/codexrig/codec"
	"github.com/rigsmith/rigsmith/internal/codexrig/config"
	"github.com/rigsmith/rigsmith/internal/codexrig/manifest"
	"github.com/rigsmith/rigsmith/internal/codexrig/rollout"
	"github.com/rigsmith/rigsmith/internal/codexrig/rolloutstore"
)

// FileRedaction names one file the redactor changed, and what it took out.
// Kinds and field paths, never values: this travels into the journal, which is
// committed, so it has to be a map of where secrets were rather than a second
// copy of them.
type FileRedaction struct {
	Rel   string   `json:"rel"`
	Kinds []string `json:"kinds,omitempty"`
	Paths []string `json:"paths,omitempty"`
	Count int      `json:"count"`
}

// OversizeFile is a file left behind for exceeding the per-file cap, with the
// size that got it dropped.
//
// The size is carried because "too large" invites exactly one question, and the
// answer decides what to do: a rollout a little over the cap argues for raising
// it, one at ten times the cap argues for leaving it behind.
type OversizeFile struct {
	Rel   string `json:"rel"`
	Bytes int64  `json:"bytes"`
}

// RootResult summarises one root's contribution.
type RootResult struct {
	ID         string
	Files      int // written this run (new or changed)
	Unchanged  int // already current in staging
	Redactions int
	Redacted   []FileRedaction
	AgedOut    int // rollouts dropped as older than the retention window
	Skipped    int // vanished or unreadable mid-sync (a live home churns)
	Oversize   []OversizeFile
	Deferred   int // large rollouts changed, but not enough yet to restage
	Disallowed int // staged files removed because the allowlist no longer permits them
	Absent     bool
}

// Report is the outcome of a sync into the staging directory.
type Report struct {
	Roots           []RootResult
	ManifestCwds    int
	RetentionPruned int
	// LedgerAdded is sessions newly recorded or re-fingerprinted; LedgerTotal
	// is how many this machine's ledger remembers, aged-out ones included.
	LedgerAdded int
	LedgerTotal int
	// LedgerError is why the ledger could not be updated. Never fatal.
	LedgerError string
	Findings    []redact.Finding // non-empty ⇒ Sync returned an error
}

// CredentialFiles counts the findings that are whole files of credential
// material rather than values inside one.
//
// Derived, never accumulated. A counter has to be right at every append and
// every early return; this reads the same list the caller is about to be handed.
func (r Report) CredentialFiles() int {
	n := 0
	for _, f := range r.Findings {
		if f.File {
			n++
		}
	}
	return n
}

// Files and Unchanged total the roots, for a caller that wants one number.
func (r Report) Files() int {
	n := 0
	for _, rr := range r.Roots {
		n += rr.Files
	}
	return n
}

func (r Report) Unchanged() int {
	n := 0
	for _, rr := range r.Roots {
		n += rr.Unchanged
	}
	return n
}

// Options configure a sync.
type Options struct {
	StagingDir string
	Config     *config.Config
	Machine    config.Machine
	// CodexVersion is stamped into the manifest, for a reader deciding whether
	// a restore is likely to work.
	CodexVersion string

	// RetentionDays drops rollouts older than this many days (0 = keep all).
	RetentionDays int
	// RedactRollouts scrubs credential-shaped tokens out of the STAGED copy of a
	// rollout. The live file is never touched.
	RedactRollouts bool
	// ChunkRollouts stores a rollout over rolloutstore.Threshold as
	// content-addressed parts, so an append costs a chunk rather than a copy.
	ChunkRollouts bool

	// MaxFileBytes drops any single file larger than this (<= 0 = no cap). Git
	// hosts reject oversized blobs and take the whole push down with them.
	MaxFileBytes int64
	// LargeFileBytes throttles how often a big append-only rollout is restaged.
	// Past this size a rollout is copied again only once it has grown by half
	// this much, or once it has been quiet for largeFileSettle — so an active
	// marathon session costs one blob per chunk of new content rather than one
	// per sync.
	LargeFileBytes int64
	// Flush names source files the throttle must not defer this run — the
	// rollout of the session that just ended, which the hook passes on. Scoped
	// rather than global on purpose: flushing everything would restage another
	// session's large rollout mid-chunk whenever any short session ended, which
	// is the growth the throttle exists to stop.
	Flush []string

	// SourceOverride maps a root id to an absolute directory, used verbatim
	// instead of resolving it from the machine. Symmetric with restore's
	// TargetOverride, and what makes the engine testable without a real ~/.codex.
	SourceOverride map[string]string
}

// largeFileSettle is how long a large rollout must go unwritten before a change
// too small to earn a restage is staged anyway. It is the fallback for a session
// that ended without its hook firing — a crash, a hook not installed. Nothing
// schedules a sync at the deadline; the rule is evaluated by whatever sync runs
// next, so it catches the tail up then rather than never.
const largeFileSettle = 30 * time.Minute

// Sync materialises the allowlisted, redacted file set for each enabled root
// into StagingDir/<root-id>/…, writes the manifest, and runs the tripwire over
// the whole staged tree. A tripwire hit fails the sync loudly, and nothing is
// pushed in that case.
func Sync(opts Options) (*Report, error) {
	if opts.Config == nil {
		return nil, errors.New("sync needs a config")
	}
	rep := &Report{}
	policy := redact.DefaultPolicy()
	lists := cxallow.Options{Sessions: opts.Config.SyncSessions}

	var cutoff time.Time
	if opts.RetentionDays > 0 {
		cutoff = time.Now().AddDate(0, 0, -opts.RetentionDays)
	}
	flush := newFlushSet(opts.Flush)

	// Turning the scrubber ON has to reach rollouts an earlier sync already
	// staged unscrubbed. Nothing about those files changes when the setting
	// does — same mtime, same source — so the incremental skip would keep
	// handing back the copy that still holds the secret until the live rollout
	// happened to change. The first run after it is enabled restages them all.
	rescrub := opts.RedactRollouts && !scrubbedLastRun(opts.StagingDir)

	man := manifest.New(opts.CodexVersion, opts.Machine.OS)
	folders := opts.Machine.Folders()

	// What this run's walk reached, so the manifest and the reconcile pass can
	// tell a file with no live source from one that simply did not change.
	audited := newAuditCache(opts.StagingDir)
	// What this run copied in, so a finding the audit raises against one of
	// them can take the copy back out again. Keyed by staged path, which is how
	// a finding names it.
	stagedThisRun := map[string]bool{}
	startedAt := time.Now()
	clock := readStageClock(opts.StagingDir)
	ticks := map[string]time.Duration{}

	for _, r := range opts.Config.Roots {
		if !r.Enabled {
			continue
		}
		rr := RootResult{ID: r.ID}
		loc, status := sourceLoc(opts, r)
		if status != pathmap.StatusResolved || !dirExists(loc) {
			rr.Absent = true
			rep.Roots = append(rep.Roots, rr)
			continue
		}

		tick, ok := ticks[loc]
		if !ok {
			tick = probeMtimeTick(loc)
			ticks[loc] = tick
		}

		list := cxallow.For(r.ID, lists)
		files, _, err := allowlist.Walk(loc, list)
		if err != nil {
			return nil, fmt.Errorf("walk %s: %w", r.ID, err)
		}
		stageRoot := filepath.Join(opts.StagingDir, r.ID)

		for _, rel := range files {
			srcPath := filepath.Join(loc, filepath.FromSlash(rel))
			dstPath := filepath.Join(stageRoot, filepath.FromSlash(rel))
			stagedRel := r.ID + "/" + rel

			info, err := os.Stat(srcPath)
			if err != nil {
				// A live ~/.codex churns under us; a file that vanished
				// mid-sync must not abort the whole sync.
				rr.Skipped++
				continue
			}

			isRollout := rollout.IsRolloutRel(rel)

			// Retention: drop rollouts older than the window.
			if !cutoff.IsZero() && isRollout && info.ModTime().Before(cutoff) {
				rr.AgedOut++
				continue
			}

			// Size cap. Remove any copy an earlier, uncapped sync staged —
			// otherwise the cap can never dig a repo out of the hole it was
			// added to fix.
			//
			// A rollout that will be stored in parts is exempt, and that is the
			// point of the exemption rather than a loophole: no blob it produces
			// exceeds one chunk, so the reason for the cap — git hosts reject
			// oversized objects and fail the whole push — does not apply. Without
			// it the largest conversation on a machine is the one thing never
			// backed up. The one measured here is 180 MB against a 50 MB cap.
			chunkable := opts.ChunkRollouts && isRollout && info.Size() > rolloutstore.Threshold
			if opts.MaxFileBytes > 0 && info.Size() > opts.MaxFileBytes && !chunkable {
				rr.Oversize = append(rr.Oversize, OversizeFile{Rel: rel, Bytes: info.Size()})
				_ = rolloutstore.Remove(dstPath)
				continue
			}

			// A file whose NAME says it is key material is refused on every
			// run, including ones the incremental skip would pass over: a
			// credential staged before this check existed must keep failing
			// until it is dealt with, rather than being hidden forever.
			if redact.ClassifyName(rel) == redact.NameKeyMaterial {
				rep.Findings = append(rep.Findings, redact.Finding{Path: stagedRel, Kind: "key-material", File: true})
				continue
			}

			if c, ok := codec.For(rel); ok {
				changed, paths, err := stageStructured(c, srcPath, dstPath, rel, policy, folders, opts.Machine.OS, &rep.Findings, stagedRel)
				switch {
				case errors.Is(err, errUnparseable):
					// A config that does not parse cannot be redacted, and
					// carrying it raw would defeat the whole guarantee. Skip
					// it; it is usually a half-written file, and the next sync
					// gets the valid one.
					rr.Skipped++
					continue
				case errors.Is(err, errRefused):
					continue
				case err != nil:
					return nil, err
				}
				if !changed {
					rr.Unchanged++
					continue
				}
				rr.Files++
				stagedThisRun[stagedRel] = true
				rr.Redactions += len(paths)
				if len(paths) > 0 {
					rr.Redacted = append(rr.Redacted, FileRedaction{Rel: rel, Paths: paths, Count: len(paths)})
				}
				continue
			}

			// Verbatim files: rollouts, skills, rules, instructions.
			scrub := opts.RedactRollouts && isRollout
			// The LOGICAL size, so a rollout kept in parts compares against its
			// source exactly as a plain one does.
			staged, serr := rolloutstore.Stat(dstPath)
			if serr != nil {
				staged = nil
			}
			// A scrubbed copy is deliberately not the same length as its source,
			// so size equality cannot be part of the test for one — it would
			// never match and the file would be rewritten on every sync forever.
			// The mtime is copied from the source exactly, so it alone already
			// means "staged from this version of this file".
			unchanged := staged != nil &&
				staged.ModTime().Equal(info.ModTime()) &&
				(scrub || staged.Size() == info.Size()) &&
				!(scrub && rescrub) &&
				clock.trusts(info.ModTime(), tick)

			if !unchanged && !rescrub && !flush.covers(srcPath) &&
				deferLarge(isRollout, info, staged, opts.LargeFileBytes, cutoff, time.Now()) {
				rr.Deferred++
				continue
			}

			if unchanged {
				// Check the bytes that will actually be published — unless the
				// audit already read exactly these bytes and cleared them.
				// Without that, every sync reads the whole staged tree twice.
				if st, err := os.Stat(dstPath); err != nil ||
					!audited.wasCleanFor(stagedRel, auditEntry{size: st.Size(), mod: st.ModTime().UnixNano()}) {
					if f := scanStaged(dstPath, rel); f != nil {
						rep.Findings = append(rep.Findings, redact.Finding{Path: stagedRel, Kind: f.Kind, File: f.File})
						continue
					}
				}
				rr.Unchanged++
				if isRollout {
					noteRolloutCwd(man, srcPath, folders, opts.Machine.OS)
				}
				continue
			}

			if scrub {
				hits, err := scrubInto(dstPath, srcPath, info.ModTime())
				switch {
				case errors.Is(err, errPrivateKey):
					rep.Findings = append(rep.Findings, redact.Finding{Path: stagedRel, Kind: "private-key", File: true})
					continue
				case errors.Is(err, errBinary):
					// Binary after a text-looking head. Fall through to the
					// verbatim copy, which carries it byte for byte — the audit
					// still reads it, so a credential in there is refused rather
					// than quietly rewritten into something unreadable.
					scrub = false
				case os.IsNotExist(err):
					rr.Skipped++
					continue
				case err != nil:
					return nil, err
				}
				if scrub {
					if len(hits) > 0 {
						rr.Redactions += len(hits)
						rr.Redacted = append(rr.Redacted, FileRedaction{Rel: rel, Kinds: kindsOf(hits), Count: len(hits)})
					}
					rr.Files++
					stagedThisRun[stagedRel] = true
					noteRolloutCwd(man, srcPath, folders, opts.Machine.OS)
					continue
				}
			}

			// Scan the EXACT bytes being staged. Reading for the scan and then
			// re-opening to copy would leave a window in which a live ~/.codex
			// replaces a benign file with a credential after it was cleared,
			// staging content that was never scanned. Files too large to hold
			// in memory are streamed and checked by the whole-tree audit.
			if info.Size() > 0 && info.Size() <= int64(redact.ScanContentLimit()) {
				data, rerr := os.ReadFile(srcPath)
				if rerr != nil {
					rr.Skipped++
					continue
				}
				if found := redact.ScanFile(rel, data); len(found) > 0 {
					rep.Findings = append(rep.Findings, redact.Finding{Path: stagedRel, Kind: found[0].Kind, File: found[0].File})
					continue
				}
				if err := writeFileMtime(dstPath, data, info.ModTime()); err != nil {
					return nil, err
				}
			} else if err := stageLarge(srcPath, dstPath, info.ModTime(), chunkable); err != nil {
				if os.IsNotExist(err) {
					rr.Skipped++
					continue
				}
				return nil, err
			}
			rr.Files++
			stagedThisRun[stagedRel] = true
			if isRollout {
				noteRolloutCwd(man, srcPath, folders, opts.Machine.OS)
			}
		}

		// Tightening the allowlist changes only which files the LIVE walk
		// offers; copies an earlier sync staged stay tracked, get re-committed,
		// and are handed back out by restore. So a rule added to keep something
		// out has no effect on what is already in the repo unless staging is
		// reconciled against it — which is what this does. Only for a root that
		// resolved here: a root we skipped tells us nothing about whether its
		// staged files are still wanted, and pruning it would delete another
		// machine's data.
		removed, err := reconcileStagedRoot(stageRoot, list)
		if err != nil {
			return nil, fmt.Errorf("reconcile staged %s: %w", r.ID, err)
		}
		rr.Disallowed = removed

		rep.Roots = append(rep.Roots, rr)
	}

	// Bring already-staged rollouts to the configured representation, so turning
	// the setting on converts what is there rather than only affecting what
	// arrives next.
	if err := convertStagedRollouts(opts.StagingDir, opts.ChunkRollouts); err != nil {
		return rep, err
	}

	// Record every staged session in the permanent ledger BEFORE retention runs.
	// The synced tree is a rolling window; the ledger is not, so a rollout that
	// is about to age out still leaves a searchable row behind. The other order
	// would make `search` answer "no such session" for a conversation that
	// simply got old, which reads as "that never happened".
	if added, total, lerr := recordLedger(opts.StagingDir, opts.Machine.Name); lerr == nil {
		rep.LedgerAdded, rep.LedgerTotal = added, total
	} else {
		// Best-effort: the ledger makes a later search better, and must never
		// cost anybody a backup.
		rep.LedgerError = lerr.Error()
	}

	// Enforce retention on the STAGED tree as well as on copy, so a rollout that
	// ages out on every machine eventually leaves the repo rather than living
	// forever because no walk offers it any more.
	if !cutoff.IsZero() {
		pruned, err := pruneAgedRollouts(opts.StagingDir, cutoff)
		if err != nil {
			return nil, err
		}
		rep.RetentionPruned = pruned
	}

	// Union with what is already there: every machine's syncs land in one repo,
	// and this machine's absence of a directory is not evidence that another
	// machine's is gone.
	if existing, err := manifest.Load(opts.StagingDir); err == nil {
		man.MergeFrom(existing)
	}
	if err := man.Save(opts.StagingDir); err != nil {
		return nil, err
	}
	rep.ManifestCwds = len(man.Cwds)

	findings, err := Audit(opts.StagingDir)
	if err != nil {
		return rep, err
	}
	seen := map[redact.Finding]bool{}
	for _, f := range rep.Findings {
		seen[f] = true
	}
	for _, f := range findings {
		if !seen[f] {
			rep.Findings = append(rep.Findings, f)
			seen[f] = true
		}
		// A file THIS run staged, which the audit then condemned, is removed
		// from the tree.
		//
		// The two scans are deliberately different: the inbound one is narrow
		// and name-led, because a false positive there aborts every future sync;
		// the audit is the real credential-signature scan over the bytes about
		// to be published. So a token pasted into a note is caught here, after
		// the copy — and leaving that copy behind means a blob holding a
		// credential sits in the working tree, one permissive run away from
		// being committed. Only files this run wrote are touched: a finding in
		// something an older codexrig staged is the user's to deal with, and
		// deleting it would hide the problem instead of reporting it.
		if stagedThisRun[f.Path] {
			if err := os.Remove(filepath.Join(opts.StagingDir, filepath.FromSlash(f.Path))); err == nil {
				delete(stagedThisRun, f.Path)
			}
		}
	}

	// Recorded here rather than past the tripwire: the staging pass has
	// finished, so every rollout in staging was written under this run's
	// setting whatever the tripwire goes on to say. Leaving the marker stale
	// would make the next run redo the whole scrub, and the one after that, for
	// as long as anything in the tree is refused.
	noteScrubSetting(opts.StagingDir, opts.RedactRollouts)
	// The walk finished, so every mtime older than this instant has now been
	// staged from — recorded even when the tripwire refuses below, because the
	// files were still copied.
	writeStageClock(opts.StagingDir, startedAt)

	if len(rep.Findings) > 0 {
		return rep, tripwireError(rep.Findings)
	}
	return rep, nil
}

// tripwireError says WHICH half of the wire fired, because the two need
// different remedies: a value means the redactor's field rules missed something,
// a whole file means it should never have been in the allowlist.
func tripwireError(findings []redact.Finding) error {
	files := 0
	for _, f := range findings {
		if f.File {
			files++
		}
	}
	switch {
	case files == len(findings):
		return fmt.Errorf("secret tripwire: %d file(s) are credential material and cannot be redacted; refusing to sync — exclude them from the allowlist or remove them", files)
	case files > 0:
		return fmt.Errorf("secret tripwire: %d credential file(s) and %d unredacted value(s); refusing to sync", files, len(findings)-files)
	default:
		return fmt.Errorf("secret tripwire: %d value(s) look like credentials and were not redacted; refusing to sync", len(findings))
	}
}

var (
	errUnparseable = errors.New("not parseable in its own format")
	errRefused     = errors.New("refused by the tripwire")
)

// stageStructured runs a configuration file through its codec: decode, drop what
// is local to this machine, redact, portablize, encode, scan, and write only if
// the bytes moved.
func stageStructured(c codec.Codec, srcPath, dstPath, rel string, policy redact.Policy,
	folders pathmap.MapFolders, srcOS string, findings *[]redact.Finding, stagedRel string) (changed bool, paths []string, err error) {
	data, err := os.ReadFile(srcPath)
	if err != nil {
		return false, nil, err
	}
	v, derr := c.Decode(data)
	if derr != nil {
		return false, nil, errUnparseable
	}
	v = dropMachineLocal(rel, v)
	v, paths = redact.Redact(v, policy)
	v, _ = pathmap.PortablizeJSONValues(v, folders, srcOS)
	// Keys as well as values: Codex addresses per-project settings BY path.
	v, _ = PortablizeKeys(v, folders, srcOS)
	out, eerr := c.Encode(v)
	if eerr != nil {
		return false, nil, errUnparseable
	}
	for _, f := range redact.Scan(v) {
		*findings = append(*findings, redact.Finding{Path: stagedRel + ":" + f.Path, Kind: f.Kind})
	}
	if len(*findings) > 0 {
		// A refused config is not staged at all. Writing it and reporting the
		// finding would leave the credential in the tree for the next run to
		// find again, forever.
		for _, f := range *findings {
			if strings.HasPrefix(f.Path, stagedRel+":") {
				return false, paths, errRefused
			}
		}
	}
	// Byte comparison rather than mtime: the output is DERIVED, so its
	// timestamp says nothing about whether the content moved. Without this
	// every config counted as written on every sync, which is what makes an
	// activity feed repeat one identical line forever.
	if prev, rerr := os.ReadFile(dstPath); rerr == nil && bytes.Equal(prev, out) {
		return false, paths, nil
	}
	if err := writeFile(dstPath, out); err != nil {
		return false, paths, err
	}
	return true, paths, nil
}

// dropMachineLocal removes the tables inside a Codex config that describe THIS
// machine and would be meaningless or misleading anywhere else.
//
// Trust entries under [projects."<abs path>"] are deliberately KEPT: they are
// portablized like any other path, and dropping them would make a restore end
// with the user re-trusting every repository by hand, which is the kind of
// friction that gets a backup tool switched off.
func dropMachineLocal(rel string, v any) any {
	if filepath.Base(rel) != "config.toml" && !strings.HasSuffix(rel, ".config.toml") {
		return v
	}
	m, ok := v.(map[string]any)
	if !ok {
		return v
	}
	out := make(map[string]any, len(m))
	for k, val := range m {
		switch k {
		case "hooks":
			// [hooks.state."<abs path>:<event>:<g>:<h>"].trusted_hash pins a
			// content hash of a file at an absolute path. Both halves are
			// local: the path may not exist elsewhere, and a stale hash grants
			// nothing. Any other key under [hooks] is real configuration and is
			// kept.
			if hm, ok := val.(map[string]any); ok {
				kept := make(map[string]any, len(hm))
				for hk, hv := range hm {
					if hk == "state" {
						continue
					}
					kept[hk] = hv
				}
				if len(kept) > 0 {
					out[k] = kept
				}
				continue
			}
			out[k] = val
		default:
			out[k] = val
		}
	}
	return out
}

// deferLarge reports whether a changed rollout should wait: it is over the
// threshold, a staged copy exists, and the source has neither grown by half the
// threshold since that copy nor gone quiet.
//
// A source no LARGER than the staged copy is never deferred — a change that did
// not add bytes is a rewrite, not an append, and the staged copy is simply
// wrong. Nor is one whose staged copy has already aged past the retention
// cutoff: retention prunes staged files later in the same sync, and a live
// rollout that was just appended must not lose its only copy to a deferral.
func deferLarge(isRollout bool, src, staged os.FileInfo, threshold int64, cutoff, now time.Time) bool {
	if !isRollout || threshold <= 0 || staged == nil || src.Size() <= threshold {
		return false
	}
	if !cutoff.IsZero() && staged.ModTime().Before(cutoff) {
		return false
	}
	grown := src.Size() - staged.Size()
	// Half the threshold, rounded up: an odd threshold must not let growth of
	// just under half through.
	if grown <= 0 || grown >= (threshold+1)/2 {
		return false
	}
	return now.Sub(src.ModTime()) < largeFileSettle
}

// flushSet is what a flush covers: the rollouts named, and nothing else. A flush
// is one session's, not the machine's.
type flushSet map[string]bool

func newFlushSet(paths []string) flushSet {
	if len(paths) == 0 {
		return nil
	}
	f := make(flushSet, len(paths))
	for _, p := range paths {
		f[canonicalPath(p)] = true
	}
	return f
}

func (f flushSet) covers(p string) bool {
	if len(f) == 0 {
		return false
	}
	return f[canonicalPath(p)]
}

// canonicalPath is a path as the flush set keys it, so a path the hook reports
// and the one the walk visits agree whatever the spelling.
func canonicalPath(p string) string {
	p = filepath.Clean(p)
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	return p
}

func noteRolloutCwd(man *manifest.Manifest, srcPath string, folders pathmap.MapFolders, srcOS string) {
	m, ok, err := rollout.ReadMeta(srcPath)
	if err != nil || !ok || m.Cwd == "" {
		return
	}
	if tmpl, ok := pathmap.Portablize(m.Cwd, folders, srcOS); ok {
		man.Note(m.Cwd, tmpl)
	}
}

func kindsOf(hits []redact.TextHit) []string {
	seen := map[string]bool{}
	var out []string
	for _, h := range hits {
		if !seen[h.Kind] {
			seen[h.Kind] = true
			out = append(out, h.Kind)
		}
	}
	sort.Strings(out)
	return out
}

func sourceLoc(opts Options, r config.Root) (string, pathmap.Status) {
	if loc, ok := opts.SourceOverride[r.ID]; ok {
		return loc, pathmap.StatusResolved
	}
	return r.ResolveOn(opts.Machine)
}

// scanStaged reads a whole staged file and fails CLOSED on a read error: an
// unreadable file in a tree about to be published is a finding, not a pass.
func scanStaged(path, rel string) *redact.Finding {
	f, err := os.Open(path)
	if err != nil {
		return &redact.Finding{Path: rel, Kind: redact.KindUnreadable, File: true}
	}
	defer f.Close()
	finding, err := redact.ScanReader(rel, f)
	if err != nil {
		return &redact.Finding{Path: rel, Kind: redact.KindUnreadable, File: true}
	}
	return finding
}

// reconcileStagedRoot deletes staged files the allowlist no longer permits, and
// the directories that empties.
func reconcileStagedRoot(stageRoot string, list allowlist.List) (int, error) {
	if !dirExists(stageRoot) {
		return 0, nil
	}
	var doomed []string
	err := filepath.WalkDir(stageRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(stageRoot, p)
		if rerr != nil {
			return rerr
		}
		rel = filepath.ToSlash(rel)
		// A part belongs to its index, not to the allowlist. Judging it on its
		// own would delete the contents of a rollout the rules still permit and
		// leave an index pointing at nothing.
		if rolloutstore.IsPartPath(rel) {
			return nil
		}
		if !list.Match(rel) {
			doomed = append(doomed, p)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	for _, p := range doomed {
		// Remove, not os.Remove: a rollout that is no longer allowed takes its
		// parts with it.
		if err := rolloutstore.Remove(p); err != nil && !os.IsNotExist(err) {
			return 0, err
		}
	}
	pruneEmptyDirs(stageRoot)
	return len(doomed), nil
}

// pruneAgedRollouts removes staged rollouts older than the cutoff, across every
// machine's shards, and the date directories they empty.
func pruneAgedRollouts(staging string, cutoff time.Time) (int, error) {
	pruned := 0
	roots, err := os.ReadDir(staging)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	for _, rootDir := range roots {
		if !rootDir.IsDir() || rootDir.Name() == ".git" {
			continue
		}
		base := filepath.Join(staging, rootDir.Name())
		err := filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				if os.IsNotExist(err) {
					return nil
				}
				return err
			}
			if d.IsDir() {
				return nil
			}
			rel, rerr := filepath.Rel(base, p)
			if rerr != nil {
				return rerr
			}
			if !rollout.IsRolloutRel(filepath.ToSlash(rel)) {
				return nil
			}
			info, ierr := d.Info()
			if ierr != nil {
				return nil
			}
			if info.ModTime().Before(cutoff) {
				if rmErr := rolloutstore.Remove(p); rmErr == nil {
					pruned++
				}
			}
			return nil
		})
		if err != nil {
			return pruned, err
		}
		pruneEmptyDirs(base)
	}
	return pruned, nil
}

// pruneEmptyDirs removes directories left empty, deepest first, leaving root.
func pruneEmptyDirs(root string) {
	var dirs []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() && p != root {
			dirs = append(dirs, p)
		}
		return nil
	})
	sort.Sort(sort.Reverse(sort.StringSlice(dirs)))
	for _, d := range dirs {
		entries, err := os.ReadDir(d)
		if err == nil && len(entries) == 0 {
			_ = os.Remove(d)
		}
	}
}

// --- the scrub-setting marker -------------------------------------------
//
// scrubVersion changes when the scrub's scope or the credential rules expand,
// forcing already-staged copies through the current redactor once.
const scrubVersion = "1"

const scrubStateName = ".scrub-state"

func scrubStatePath(staging string) string {
	if staging == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(staging), scrubStateName)
}

func scrubbedLastRun(staging string) bool {
	p := scrubStatePath(staging)
	if p == "" {
		return false
	}
	b, err := os.ReadFile(p)
	return err == nil && strings.TrimSpace(string(b)) == scrubVersion
}

// noteScrubSetting records what this sync ran with. Best-effort: a marker that
// cannot be written costs a needless restage next time, which is the harmless
// direction.
func noteScrubSetting(staging string, on bool) {
	p := scrubStatePath(staging)
	if p == "" {
		return
	}
	v := []byte("0\n")
	if on {
		v = []byte(scrubVersion + "\n")
	}
	_ = os.WriteFile(p, v, 0o644)
}

// --- file helpers --------------------------------------------------------

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func writeFileMtime(path string, data []byte, mod time.Time) error {
	if err := writeFile(path, data); err != nil {
		return err
	}
	return os.Chtimes(path, mod, mod)
}

// copyPreservingMtime streams a file into staging, via a temp sibling and a
// rename so a reader never sees a partial copy, and carries the source's mtime
// so the incremental skip can recognise it next run.
func copyPreservingMtime(src, dst string, mod time.Time) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), "."+filepath.Base(dst)+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, 0o644); err != nil {
		return err
	}
	if err := os.Rename(name, dst); err != nil {
		return err
	}
	return os.Chtimes(dst, mod, mod)
}

// stageLarge copies a rollout into staging in whichever representation is
// asked for.
func stageLarge(src, dst string, mod time.Time, chunked bool) error {
	if !chunked {
		return copyPreservingMtime(src, dst, mod)
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	return rolloutstore.Write(dst, in, mod)
}

// convertStagedRollouts brings every staged rollout to the configured
// representation.
//
// Turning the setting on has to reach what is ALREADY in the repo — which is
// where the large conversations are. Nothing about those files changes when the
// setting does, so the incremental skip would leave them as single blobs until
// each one happened to be written to again.
func convertStagedRollouts(staging string, chunked bool) error {
	roots, err := os.ReadDir(staging)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, rootDir := range roots {
		if !rootDir.IsDir() || rootDir.Name() == ".git" {
			continue
		}
		base := filepath.Join(staging, rootDir.Name())
		werr := filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				if os.IsNotExist(err) {
					return nil
				}
				return err
			}
			if d.IsDir() {
				return nil
			}
			rel, rerr := filepath.Rel(base, p)
			if rerr != nil {
				return rerr
			}
			if !rollout.IsRolloutRel(filepath.ToSlash(rel)) {
				return nil
			}
			_, cerr := rolloutstore.Convert(p, chunked)
			return cerr
		})
		if werr != nil {
			return werr
		}
	}
	return nil
}
