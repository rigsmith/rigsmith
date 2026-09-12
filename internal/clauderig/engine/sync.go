// Package engine orchestrates the sync pipeline: walk each root's allowlist,
// redact secrets, copy into the staging repo, build the project manifest, and run
// the secret tripwire. Pure of git — the caller commits/pushes the staging dir
// via internal/gitrepo. This is where the standalone units (allowlist, redact,
// manifest) compose into the actual operation.
package engine

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rigsmith/rigsmith/core/pathmap"
	"github.com/rigsmith/rigsmith/internal/clauderig/allowlist"
	"github.com/rigsmith/rigsmith/internal/clauderig/backupgit"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/desktop"
	"github.com/rigsmith/rigsmith/internal/clauderig/manifest"
	"github.com/rigsmith/rigsmith/internal/clauderig/redact"
	"github.com/rigsmith/rigsmith/internal/clauderig/transcript"
)

// FileRedaction names one file the redactor changed on the way into staging,
// and what it took out. Kinds, never values: this travels into the journal,
// which is synced, so it has to be a map of where secrets were rather than a
// second copy of them.
type FileRedaction struct {
	Rel string `json:"rel"`
	// Kinds names what was taken out — "anthropic-key", "bearer" — for the
	// transcript scrubber, which classifies each hit as it finds it.
	Kinds []string `json:"kinds,omitempty"`
	// Paths names WHERE it was, as dotted JSON field paths, for structured
	// files. The JSON redactor works by field and has no notion of kind, and
	// reporting `env.API_KEY` as a kind made the two halves of this report
	// disagree about what the field meant.
	Paths []string `json:"paths,omitempty"`
	Count int      `json:"count"`
}

// OversizeFile is one file left out of the sync for exceeding MaxFileBytes,
// with the size that got it dropped.
//
// The size is carried because "too large" invites exactly one question, and the
// answer decides what to do: a transcript a little over the cap is an argument
// for raising it, one at ten times the cap is an argument for leaving it behind.
// A report that cannot answer that leaves the reader with nothing to act on.
type OversizeFile struct {
	Rel   string `json:"rel"`
	Bytes int64  `json:"bytes"`
}

// RootResult summarises one root's contribution to a sync.
type RootResult struct {
	ID         string
	Files      int // files written this sync (new or changed)
	Unchanged  int // files already current in staging (incremental skip)
	Redactions int
	// Redacted names the files behind Redactions. "21 secrets redacted" answers
	// how many; the only useful follow-up is which, and the count alone could
	// not be acted on.
	Redacted       []FileRedaction
	RetentionByAge int            // project transcripts dropped as older than the window
	SkippedFiles   int            // files that vanished/were unreadable mid-sync (live churn)
	Oversize       []OversizeFile // files dropped for exceeding MaxFileBytes
	Deferred       int            // large transcripts changed since staged, but not enough yet to restage (see LargeFileBytes)
	Disallowed     int            // staged files removed because the allowlist no longer permits them
	Skipped        bool           // root absent on this machine
}

// Report is the outcome of a sync into the staging dir.
type Report struct {
	Roots            []RootResult
	ManifestProjects int
	RetentionPruned  int              // staged transcript files removed as aged-out
	OrphansScrubbed  int              // staged transcripts scrubbed with no live source left
	LedgerAdded      int              // sessions newly recorded (or re-fingerprinted) in the ledger
	LedgerTotal      int              // sessions the ledger remembers, including aged-out ones
	SidecarsPruned   int              // staged Desktop sidecars removed as orphaned
	LedgerError      string           // why the ledger could not be updated ("" = fine); never fatal
	Findings         []redact.Finding // non-empty ⇒ Sync returned an error (tripwire)
}

// CredentialFiles counts the findings that are whole files of credential
// material rather than values inside one.
//
// Derived, never accumulated. A counter has to be right at every append and
// every early return; this cannot be wrong, because it reads the same list the
// caller is about to be handed.
func (r Report) CredentialFiles() int {
	n := 0
	for _, f := range r.Findings {
		if f.File {
			n++
		}
	}
	return n
}

// Options configure a sync.
type Options struct {
	// ChunkTranscripts uses versioned staging chunks for large transcripts.
	ChunkTranscripts bool
	StagingDir       string
	Config           *config.Config
	Machine          config.Machine
	ClaudeVersion    string
	// RetentionDays drops project transcripts older than this many days (0 = keep
	// all). Now() is the reference; the cutoff is computed once per sync.
	RetentionDays int
	// RedactTranscripts scrubs credential-shaped tokens out of the staged copy of
	// a transcript. The live file is never touched. See config.RedactTranscripts.
	RedactTranscripts bool

	// MaxFileBytes drops any single file larger than this (<= 0 = no cap). Git
	// hosts reject oversized blobs and take the whole push down with them.
	MaxFileBytes int64
	// LargeFileBytes throttles how often a big, append-only transcript is
	// restaged (<= 0 = every change, as for any other file). A transcript over
	// this size whose staged copy exists is copied again only once it has grown
	// by at least half of LargeFileBytes since, or once it has been quiet for
	// largeFileSettle — so an active marathon session costs one blob per chunk
	// of new content rather than one per sync, and a finished one is still
	// captured whole within the settle window.
	LargeFileBytes int64
	// Flush names source files the throttle must not defer this run, as
	// absolute paths — the transcript of the session that just ended, which
	// the SessionEnd hook passes on. Scoped rather than global on purpose: a
	// flush of every transcript would restage another session's large one
	// mid-chunk each time any short session ended, which is the growth the
	// throttle exists to stop. Symlinks are resolved before comparing.
	Flush []string
	// SourceOverride maps a root id to an absolute source dir, used verbatim
	// instead of resolving the root location via the machine. The machine still
	// drives path translation (portablize/manifest); this only decouples WHERE the
	// files are read from — symmetric with restore's TargetOverride.
	SourceOverride map[string]string
	// Profiles names the Claude Desktop profiles to sync alongside the configured
	// roots — see profiles.go. Each is walked as its own root, and they follow
	// the Desktop root's enabled flag.
	Profiles []string
	// LiveAccountUUID is the account this machine is logged into Claude Code as,
	// used to attribute ledger rows that no Desktop sidecar covers. Empty (not
	// logged in, unreadable) simply leaves those rows unattributed — a guess is
	// never invented, and a stored attribution is never overwritten by one.
	LiveAccountUUID string
}

// largeFileSettle is how long a large transcript has to go unwritten before a
// change too small to earn a restage on its own is staged anyway. It is the
// fallback for a session that ended without its SessionEnd hook firing (a
// crash, a hook not installed): nothing schedules a sync at the deadline —
// the rule is only evaluated by whatever sync runs next — so it catches the
// tail up then, rather than never. A session that ends normally does not
// wait for it: the hook runs `sync --flush` with that session's transcript.
const largeFileSettle = 30 * time.Minute

// flushSet resolves Options.Flush into a set keyed the way the walk will ask
// — cleaned, with symlinks resolved where the path exists — so a path the
// hook reports and the one the walk visits agree whatever the spelling.
func flushSet(paths []string) flushScope {
	var f flushScope
	if len(paths) == 0 {
		return f
	}
	f.files = make(map[string]bool, len(paths))
	for _, p := range paths {
		c := canonicalPath(p)
		f.files[c] = true
		// A session's sub-agent transcripts live beside it, under a
		// directory of its own name: projects/<slug>/<id>/subagents/….
		// They ended with the session, and the hook names only the parent.
		if dir := strings.TrimSuffix(c, ".jsonl"); dir != c {
			f.dirs = append(f.dirs, dir+string(filepath.Separator))
		}
	}
	return f
}

// flushScope is what a flush covers: the transcripts named, and every
// transcript under the directory a named session keeps its sub-agents in.
// Nothing else — a flush is one session's, not the machine's.
type flushScope struct {
	files map[string]bool
	dirs  []string
}

// covers reports whether the flush exempts path from the throttle.
func (f flushScope) covers(path string) bool {
	if len(f.files) == 0 {
		return false
	}
	c := canonicalPath(path)
	if f.files[c] {
		return true
	}
	for _, d := range f.dirs {
		if strings.HasPrefix(c, d) {
			return true
		}
	}
	return false
}

// canonicalPath is a path as the flush set keys it.
func canonicalPath(p string) string {
	p = filepath.Clean(p)
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	return p
}

// deferLarge reports whether a changed transcript should wait: it is a
// project transcript over the large-file threshold, a staged copy exists, and
// the source has neither grown by half the threshold since that copy nor
// settled. Only `projects/<slug>/….jsonl` qualifies — the append-only files
// the throttle is about; a .jsonl under a skill or plugin is ordinary data and
// syncs on every change. A source no LARGER than the staged copy is never
// deferred — a change that did not add bytes is a rewrite, not an append, and
// the staged copy is simply wrong. Nor is one whose staged copy has aged past
// the retention cutoff:
// retention prunes staged files by their mtime later in the same sync, and a
// live transcript that was just appended must not lose its only copy to a
// deferral.
func deferLarge(rel string, src, staged os.FileInfo, threshold int64, cutoff, now time.Time) bool {
	if threshold <= 0 || staged == nil || src.Size() <= threshold || !isTranscriptRel(rel) {
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

// isTranscriptRel reports whether rel is a session transcript: a .jsonl under
// projects/, memory excluded.
func isTranscriptRel(rel string) bool {
	return strings.HasPrefix(rel, "projects/") && strings.HasSuffix(rel, ".jsonl") && !isMemoryRel(rel)
}

// Sync materialises the allowlisted, redacted file set for each enabled root into
// StagingDir/<root-id>/…, writes the project manifest, and runs the tripwire over
// all staged text, including complete transcripts. A tripwire hit fails the
// sync loudly; nothing is pushed in that case.
func Sync(opts Options) (*Report, error) {
	if _, err := transcript.Enabled(opts.StagingDir); err != nil {
		return nil, err
	}
	if opts.ChunkTranscripts && opts.MaxFileBytes > 0 && opts.MaxFileBytes < transcript.ChunkSize {
		return nil, fmt.Errorf("chunkTranscripts requires retention.maxFileBytes of at least %d bytes or no cap", transcript.ChunkSize)
	}
	if !opts.ChunkTranscripts {
		if err := transcript.CheckNativeLimit(opts.StagingDir, opts.MaxFileBytes); err != nil {
			return nil, err
		}
	}
	rep := &Report{}
	// Findings from whole files, tracked apart from JSON-value findings because the
	// two need different remedies in the error message.
	policy := redact.DefaultPolicy()

	var cutoff time.Time
	if opts.RetentionDays > 0 {
		cutoff = time.Now().AddDate(0, 0, -opts.RetentionDays)
	}
	flush := flushSet(opts.Flush)

	// Turning redaction ON has to reach the transcripts an earlier sync already
	// staged unscrubbed. Nothing about those files changes when the setting
	// does — same mtime, same source — so the incremental skip below would keep
	// handing back the copy that still holds the secret, until the live
	// transcript happened to change. The first run after it is enabled restages
	// every transcript instead.
	rescrub := opts.RedactTranscripts && !redactedLastRun(opts.StagingDir)

	// Shared-memory symlinks found under the CLI root (worktree slugs linking
	// memory/ to their main project); recorded in the manifest for restore.
	var cliLinks []allowlist.Link
	// Session ids this machine's OWN root offered this run. The ledger's
	// live-account fallback is confined to these: recordLedger walks the shared
	// staged tree, which holds every machine's transcripts, so attributing on
	// presence there would have the first machine to sync claim sessions it
	// never ran — permanently, since attribution is sticky.
	var cliSessionIDs map[string]bool
	// Staged paths this run's walk reached, keyed by absolute staged path. What
	// is NOT in here after every root has run is a staged file with no live
	// source behind it any more — see sweepOrphanedTranscripts.
	visited := map[string]bool{}
	// What this run actually COPIED IN, keyed the way a finding names it
	// (<root>/<rel>). The audit below runs over the whole tree and can condemn a
	// file this run staged; this is what lets that copy be taken back out again,
	// without touching one an older clauderig left behind.
	stagedThisRun := map[string]bool{}
	// Condemned copies this run staged and then failed to remove again.
	var stranded []string
	// What the last audit read and found clean, so the unchanged path below can
	// skip re-reading bytes nothing has touched since. Never written here.
	audited := newAuditCache(opts.StagingDir)
	// When this run began, and when the last one did. An mtime is only evidence
	// of a file's contents while it is older than the run that read it; see
	// stageclock.go.
	startedAt := time.Now()
	clock := readStageClock(opts.StagingDir)
	// The mtimes being judged come from the source roots, so that is what gets
	// measured — a root on a network share or a removable disk can be far
	// coarser than the staging tree, and measuring staging would answer a
	// question about the wrong filesystem. Cached by directory: roots usually
	// share one, and the probe writes a file each time.
	ticks := map[string]time.Duration{}

	for _, r := range EffectiveRoots(opts.Config, opts.Profiles) {
		if !r.Enabled {
			continue
		}
		rr := RootResult{ID: r.ID}
		loc, st := sourceLoc(opts, r)
		if st != pathmap.StatusResolved || !dirExists(loc) {
			rr.Skipped = true
			rep.Roots = append(rep.Roots, rr)
			continue
		}

		mtimeTick, ok := ticks[loc]
		if !ok {
			mtimeTick = probeMtimeTick(loc)
			ticks[loc] = mtimeTick
		}
		files, links, err := allowlist.Walk(loc, allowlist.For(r.ID))
		if err != nil {
			return nil, fmt.Errorf("walk %s: %w", r.ID, err)
		}
		if r.ID == "cli" {
			cliLinks = links
			cliSessionIDs = sessionIDsFrom(files)
		}
		stageRoot := filepath.Join(opts.StagingDir, r.ID)

		for _, rel := range files {
			srcPath := filepath.Join(loc, filepath.FromSlash(rel))
			dstPath := filepath.Join(stageRoot, filepath.FromSlash(rel))
			isJSON := strings.HasSuffix(rel, ".json")
			// Whatever this run decides to do with it — copy, skip, defer,
			// refuse — the walk has seen it, and the orphan sweep below must
			// not go over it a second time.
			visited[dstPath] = true

			info, err := os.Stat(srcPath)
			if err != nil {
				// The live ~/.claude churns under us; a file that vanished mid-sync
				// must not abort the whole sync — skip it.
				rr.SkippedFiles++
				continue
			}

			// Retention: drop project transcripts older than the window. Memory is
			// exempt — see isMemoryRel.
			if !cutoff.IsZero() && strings.HasPrefix(rel, "projects/") && !isMemoryRel(rel) && info.ModTime().Before(cutoff) {
				rr.RetentionByAge++
				continue
			}

			// Size cap: a single oversized file (a marathon transcript) is rejected by
			// the host and fails the entire push, so drop it here. Remove any copy an
			// earlier, uncapped sync staged — otherwise the cap can never dig a repo
			// out of the hole it was added to fix.
			if opts.MaxFileBytes > 0 && info.Size() > opts.MaxFileBytes && !(opts.ChunkTranscripts && isTranscriptRel(rel) && info.Size() > 2*transcript.ChunkSize) {
				rr.Oversize = append(rr.Oversize, OversizeFile{Rel: rel, Bytes: info.Size()})
				_ = os.Remove(dstPath)
				continue
			}

			// Non-JSON (transcripts, skill files): copy verbatim, but skip if the
			// staging copy is already current (same size+mtime) — incremental sync.
			if !isJSON {
				noteFinding := func(f *redact.Finding) {
					rep.Findings = append(rep.Findings, redact.Finding{
						Path: r.ID + "/" + f.Path, Kind: f.Kind, File: f.File,
					})
				}
				// Redaction can shrink a chunk-eligible source into a native
				// snapshot. Apply the physical-file cap to those resulting bytes.
				dropOversizeSnapshot := func() (bool, error) {
					st, err := os.Stat(dstPath)
					if err != nil {
						return false, err
					}
					if opts.MaxFileBytes <= 0 || st.Size() <= opts.MaxFileBytes ||
						(opts.ChunkTranscripts && isTranscriptRel(rel) && st.Size() > 2*transcript.ChunkSize) {
						return false, nil
					}
					if err := os.Remove(dstPath); err != nil {
						return false, err
					}
					if err := os.RemoveAll(dstPath + transcript.Suffix); err != nil {
						return false, err
					}
					rr.Oversize = append(rr.Oversize, OversizeFile{Rel: rel, Bytes: st.Size()})
					return true, nil
				}
				// The name rule needs no content, so it runs on EVERY file, including
				// ones the incremental skip below won't recopy: a credential staged by
				// an earlier sync (or before this check existed) must keep failing until
				// it is dealt with, rather than being hidden forever by that skip.
				if redact.ClassifyName(rel) == redact.NameKeyMaterial {
					noteFinding(&redact.Finding{Path: rel, Kind: "key-material", File: true})
					continue
				}

				// A scrubbed transcript is deliberately not the same length as its
				// source, so size equality can't be part of the test for one —
				// it would never match and the file would be re-scrubbed on every
				// sync forever. The mtime is copied from the source exactly, so
				// it alone already means "staged from this version of this file".
				scrub := opts.RedactTranscripts && scrubbable(rel, srcPath)
				unchanged := false
				staged, derr := transcript.Stat(dstPath)
				if derr != nil {
					staged = nil
				}
				if staged != nil && staged.ModTime().Equal(info.ModTime()) &&
					(scrub || staged.Size() == info.Size()) &&
					!(scrub && rescrub) &&
					clock.trusts(info.ModTime(), mtimeTick) {
					unchanged = true
				}
				// A long session's transcript is the one file that is both large
				// and rewritten on every sync, and every copy of it is a blob the
				// repo carries until the next squash. Past LargeFileBytes it waits
				// for a chunk's worth of new content, or for the session to go
				// quiet, before it is restaged.
				if !opts.ChunkTranscripts && !rescrub && !unchanged && !flush.covers(srcPath) && deferLarge(rel, info, staged, opts.LargeFileBytes, cutoff, time.Now()) {
					rr.Deferred++
					continue
				}
				if unchanged {
					if scrub {
						dropped, err := dropOversizeSnapshot()
						if err != nil {
							return nil, err
						}
						if dropped {
							continue
						}
					}
					// Check the bytes that will actually be published — unless the
					// audit already read exactly these bytes and found them
					// clean. Without that, every sync re-read the whole staged
					// tree here as well as in the audit: two full passes over
					// gigabytes to conclude that three files had moved.
					//
					// Read-only: the audit writes those verdicts, and only when
					// it finds nothing anywhere. So this skips a file that has
					// been read at this exact size and mtime, and nothing else —
					// a credential staged by an older clauderig still fails here
					// until it is dealt with.
					if st, serr := os.Stat(dstPath); serr != nil ||
						!audited.wasClean(r.ID+"/"+rel, auditEntry{size: st.Size(), mod: st.ModTime().UnixNano()}) {
						if f := scanNonJSON(dstPath, rel); f != nil {
							noteFinding(f)
							continue
						}
					}
					rr.Unchanged++
					continue
				}

				// Optional scrubbing cleans the staged copy. The final audit still
				// checks its complete contents before publication.
				if scrub {
					hits, rerr := redactTranscript(dstPath, srcPath, info.ModTime())
					switch {
					case errors.Is(rerr, errPrivateKeyInTranscript):
						noteFinding(&redact.Finding{Path: rel, Kind: "private-key", File: true})
						continue
					case errors.Is(rerr, errBinaryContent):
						// Binary after a text-looking head. Fall through to the
						// ordinary copy below, which carries it byte for byte —
						// the audit still reads it, so a credential in there is
						// refused rather than quietly rewritten.
						scrub = false
					case os.IsNotExist(rerr):
						rr.SkippedFiles++
						continue
					case rerr != nil:
						return nil, rerr
					}
					// Only when the rewrite actually happened. Binary content
					// clears the flag above and falls through to the copy below;
					// counting it here would record a file as staged that this
					// branch never wrote.
					if scrub {
						dropped, err := dropOversizeSnapshot()
						if err != nil {
							return nil, err
						}
						if dropped {
							continue
						}
						if len(hits) > 0 {
							rr.Redactions += len(hits)
							rr.Redacted = append(rr.Redacted, FileRedaction{
								Rel: rel, Kinds: kindsOf(hits), Count: len(hits),
							})
						}
						rr.Files++
						stagedThisRun[r.ID+"/"+rel] = true
						continue
					}
				}

				// Scan the EXACT bytes being staged. Reading for the scan and then
				// re-opening to copy would leave a window in which a live ~/.claude
				// replaces a benign file with a credential after it was cleared, staging
				// content that was never scanned. Large files are copied as streams
				// and checked by the complete staged-tree audit below.
				if info.Size() > 0 && info.Size() <= int64(redact.ScanContentLimit()) {
					data, rerr := os.ReadFile(srcPath)
					if rerr != nil {
						// Unreadable is the same churn case the copy path tolerates; it
						// stages nothing, so nothing unscanned can escape this way.
						rr.SkippedFiles++
						continue
					}
					if found := redact.ScanFile(rel, data); len(found) > 0 {
						noteFinding(&found[0])
						continue
					}
					if err := writeFileMtime(dstPath, data, info.ModTime()); err != nil {
						return nil, err
					}
				} else if err := copyTranscriptSnapshot(srcPath, dstPath, info.ModTime(), opts.ChunkTranscripts && isTranscriptRel(rel) && info.Size() > 2*transcript.ChunkSize); err != nil {
					if os.IsNotExist(err) {
						rr.SkippedFiles++
						continue
					}
					return nil, err
				}
				rr.Files++
				stagedThisRun[r.ID+"/"+rel] = true
				continue
			}

			// JSON: redact secret-bearing fields (nested MCP/oauth configs carry real
			// tokens), portablize path values, scan — regenerated each sync (small).
			data, err := os.ReadFile(srcPath)
			if err != nil {
				rr.SkippedFiles++
				continue
			}
			var v any
			if json.Unmarshal(data, &v) != nil {
				// A .json that doesn't parse can't be redacted or scanned — syncing it
				// raw would defeat the "secrets never leave the machine" guarantee. Skip
				// it (it's likely a half-written file; the next sync gets the valid one).
				rr.SkippedFiles++
				continue
			}
			v = applyKeepFilter(r.ID, rel, v)
			// Counted below, not here: every JSON file in the tree is redacted on
			// every pass, so tallying at this point reported the whole tree's
			// secret count on every run — "21 secrets redacted" beside a sync
			// that wrote one file, and the same 21 on the row above and below.
			// It belongs to the files this run actually staged.
			red, paths := redact.Redact(v, policy)
			v = red
			v, _ = desktop.PortablizeJSONPaths(v, opts.Machine.Folders(), opts.Machine.OS)
			out, e := json.MarshalIndent(v, "", "  ")
			if e != nil {
				rr.SkippedFiles++
				continue
			}
			out = append(out, '\n')
			condemned := false
			for _, f := range redact.Scan(v) {
				rep.Findings = append(rep.Findings, redact.Finding{
					Path: r.ID + "/" + rel + ":" + f.Path, Kind: f.Kind,
				})
				condemned = true
			}
			// Refused, so not staged. The sync fails either way, but writing it
			// first replaces a staged copy that may have been clean with one
			// that is not — and leaves that copy in the tree for every later run
			// to find, long after the live file has been dealt with.
			if condemned {
				continue
			}
			// Compare before writing. A JSON file is regenerated on every sync —
			// read, redacted, portablized, re-marshalled — so without this every
			// one of them counted as "written" whether or not anything changed.
			// That made Files a constant floor rather than a measure of change,
			// which is what left the activity feed repeating one identical line
			// forever, and it rewrote a couple of thousand files an hour for
			// nothing.
			//
			// Byte comparison rather than mtime: the output is derived, so its
			// timestamp says nothing about whether the content moved.
			if prev, rerr := os.ReadFile(dstPath); rerr == nil && bytes.Equal(prev, out) {
				rr.Unchanged++
				continue
			}
			if err := writeFile(dstPath, out); err != nil {
				return nil, err
			}
			rr.Files++
			rr.Redactions += len(paths)
			if len(paths) > 0 {
				rr.Redacted = append(rr.Redacted, FileRedaction{
					Rel: rel, Paths: paths, Count: len(paths),
				})
			}
		}
		// Tightening the allowlist only changes which files the LIVE walk offers;
		// copies an earlier sync already staged stay tracked, get re-committed and
		// pushed, and are handed back out by restore. So a rule added to keep
		// something out has no effect on the data already in the repo unless
		// staging is reconciled against it — which is what this does.
		//
		// Only for roots that resolved on this machine: a root we skipped tells us
		// nothing about whether its staged files are still wanted, and pruning it
		// would delete another machine's data.
		disallowed, perr := reconcileStagedRoot(stageRoot, allowlist.For(r.ID))
		if perr != nil {
			return nil, fmt.Errorf("reconcile staged %s: %w", r.ID, perr)
		}
		rr.Disallowed = disallowed

		rep.Roots = append(rep.Roots, rr)
	}

	if err := transcript.ConvertTree(opts.StagingDir, opts.ChunkTranscripts); err != nil {
		return rep, err
	}

	// Record every staged session in the permanent ledger BEFORE retention runs.
	// The synced tree is a rolling window; the ledger is not, so a transcript that
	// is about to age out still leaves a searchable row behind — otherwise `search`
	// answers "no such session", which reads as "that chat never existed" rather
	// than "its body is older than the window, recover it from git history".
	if added, total, lerr := recordLedger(opts.StagingDir, opts.Machine.Name, opts.LiveAccountUUID, cliSessionIDs); lerr == nil {
		rep.LedgerAdded, rep.LedgerTotal = added, total
	} else {
		// Best-effort: the ledger is a convenience for later searches and must
		// never cost anyone a sync.
		rep.LedgerError = lerr.Error()
	}

	// Enforce the rolling retention window on the STAGING tree, not just on copy:
	// remove staged transcript files older than the cutoff (across all machines'
	// slugs) and the dirs they empty. This also ages out projects deleted or gone
	// idle on any machine, so stale slugs don't accumulate forever.
	var stagedSlugs map[string]bool
	if !cutoff.IsZero() {
		pruned, remaining, err := pruneAgedStagedProjects(filepath.Join(opts.StagingDir, "cli", "projects"), cutoff)
		if err != nil {
			return nil, err
		}
		rep.RetentionPruned, stagedSlugs = pruned, remaining
	}

	// Sidecars go last, after transcript retention above has settled which
	// transcripts survive — that ordering is what makes the two trees age out as
	// one unit instead of on independent clocks.
	//
	// Only when the CLI root actually synced this run. Staging keeps transcripts
	// from earlier syncs and other machines, so the index is rarely empty even
	// when this machine contributed nothing — and treating that stale set as
	// authoritative would let a Desktop-only sync delete the very sidecars it just
	// copied, whose transcripts were never offered to this run. That is the churn
	// the "no thrash" rule exists to prevent, so the emptiness check alone is not
	// enough of a guard.
	if cliSynced(rep) {
		sidecarsPruned, err := pruneOrphanedSidecars(opts.StagingDir, desktopTreesIn(rep))
		if err != nil {
			return nil, err
		}
		rep.SidecarsPruned = sidecarsPruned
	}

	// Build the project manifest from the CLI root's projects dir.
	if cliLoc, st := cliSourceLoc(opts); st == pathmap.StatusResolved {
		projects := filepath.Join(cliLoc, "projects")
		if dirExists(projects) {
			m, err := manifest.Build(projects, opts.ClaudeVersion, opts.Machine.OS, opts.Machine.Folders())
			if err != nil {
				return nil, fmt.Errorf("manifest: %w", err)
			}
			mySlugs := make(map[string]bool, len(m.Projects))
			for slug := range m.Projects {
				mySlugs[slug] = true
			}
			links := make(map[string]string, len(cliLinks))
			for _, lk := range cliLinks {
				links[lk.Rel] = lk.Target
			}
			// Union with the existing manifest so other machines' projects (whose
			// files persist in staging) keep their entries — this machine's local
			// projects are authoritative for their own slugs; others are preserved.
			// Links union the same way: a link rooted in one of this machine's
			// slugs is authoritative here (its absence means it was removed).
			if existing, err := manifest.Load(opts.StagingDir); err == nil {
				for slug, p := range existing.Projects {
					if _, mine := m.Projects[slug]; !mine {
						m.Projects[slug] = p
					}
				}
				for rel, tgt := range existing.Links {
					if s := linkSlug(rel); s != "" && mySlugs[s] {
						continue
					}
					if _, mine := links[rel]; !mine {
						links[rel] = tgt
					}
				}
			}
			// Drop entries whose staged files were just pruned away (no transcripts left).
			if stagedSlugs != nil {
				for slug := range m.Projects {
					if !stagedSlugs[slug] {
						delete(m.Projects, slug)
					}
				}
			}
			// A link only makes sense while both its endpoints' projects are in the
			// manifest — a pruned or deleted project takes its links along.
			for rel, tgt := range links {
				if s := linkSlug(rel); s != "" && !projectIn(m, s) {
					delete(links, rel)
					continue
				}
				if s := linkSlug(tgt); s != "" && !projectIn(m, s) {
					delete(links, rel)
				}
			}
			if len(links) > 0 {
				m.Links = links
			}
			if err := m.Save(opts.StagingDir); err != nil {
				return nil, err
			}
			rep.ManifestProjects = len(m.Projects)
		}
	}

	// A staged transcript whose live source is gone is never walked again, so it
	// keeps whatever it held when it was staged. Staged before redaction was
	// turned on, that is a credential the tripwire finds on every sync from here
	// to forever — and turning the setting on cannot clear it, because there is
	// no source left to re-scrub from. Scrub the staged copy itself instead.
	//
	// Only on the run that turns redaction on, which is the run that owes the
	// tree a full pass anyway. Orphans staged while it was already on were
	// scrubbed on the way in.
	if rescrub {
		swept, err := sweepOrphanedTranscripts(opts.StagingDir, visited)
		if err != nil {
			return nil, err
		}
		rep.OrphansScrubbed = swept
	}

	if err := backupgit.Ensure(opts.StagingDir); err != nil {
		return rep, err
	}
	if audit, err := Audit(opts.StagingDir); err != nil {
		return rep, err
	} else {
		seen := make(map[redact.Finding]bool)
		for _, f := range rep.Findings {
			seen[f] = true
		}
		for _, f := range audit {
			if !seen[f] {
				rep.Findings = append(rep.Findings, f)
				seen[f] = true
			}
			// A file THIS run copied in, which the audit then condemned, is
			// taken back out.
			//
			// The two scans are deliberately different: the inbound one is
			// narrow and name-led, because a false positive there refuses every
			// future sync; the audit is the full credential-signature scan over
			// the bytes about to be published. So a token pasted into a
			// transcript is caught only AFTER the copy — and leaving that copy
			// behind means a blob holding a credential sits in the working tree,
			// one permissive run away from being committed.
			//
			// Only files this run wrote. A finding in something an older
			// clauderig staged is the user's to deal with, and deleting it would
			// hide the problem rather than report it.
			if stagedThisRun[f.Path] {
				// A failure here is the one outcome worth saying out loud: the
				// condemned bytes are still in the tree, which is the state
				// this removal exists to prevent. Keep going — every other
				// condemned file still has to be tried — and report at the end.
				if err := removeStaged(opts.StagingDir, f.Path); err != nil {
					stranded = append(stranded, fmt.Sprintf("%s: %v", f.Path, err))
				} else {
					delete(stagedThisRun, f.Path)
				}
			}
		}
	}
	// Recorded here rather than past the tripwire: by this point the staging
	// pass has finished, so every transcript in staging was written under this
	// run's setting whatever the tripwire goes on to say. A refusal is about
	// what may be published, not about what was scrubbed — and leaving the
	// marker stale would make the next run redo the whole scrub, and the one
	// after that, for as long as anything in the tree is refused. A run that
	// genuinely stopped part-way returns above this and still re-scrubs.
	noteRedactionSetting(opts.StagingDir, opts.RedactTranscripts)
	// The walk finished, so every mtime older than this instant has now been
	// staged from. Recorded even when the tripwire refuses below: the files
	// were still copied, and the reason to distrust their mtimes is gone.
	writeStageClock(opts.StagingDir, startedAt)
	if len(rep.Findings) > 0 {
		// A condemned copy that could not be deleted outranks the tripwire text:
		// the refusal alone reads as "nothing left the machine", and here
		// something is still sitting in the staging tree.
		if len(stranded) > 0 {
			return rep, fmt.Errorf("secret tripwire: %d value(s) look like credentials, and %d condemned file(s) could not be removed from staging — delete them by hand before the next sync: %s",
				len(rep.Findings), len(stranded), strings.Join(stranded, "; "))
		}
		// The two halves of the wire need different remedies, so say which one
		// fired: a JSON value means the redactor's key rules missed something, a
		// whole file means it should never have been in the allowlist.
		files := rep.CredentialFiles()
		switch {
		case files == len(rep.Findings):
			return rep, fmt.Errorf("secret tripwire: %d file(s) are credential material and cannot be redacted; refusing to sync — exclude them from the allowlist or remove them", files)
		case files > 0:
			return rep, fmt.Errorf("secret tripwire: %d credential file(s) and %d unredacted value(s); refusing to sync", files, len(rep.Findings)-files)
		default:
			return rep, fmt.Errorf("secret tripwire: %d value(s) look like credentials and were not redacted; refusing to sync", len(rep.Findings))
		}
	}
	return rep, nil
}

// redactionVersion changes when the scrub scope or credential rules expand,
// forcing existing staged copies through the current redactor once.
const redactionVersion = "2"

// redactionStatePath is where the last run's redactTranscripts setting is kept.
// Beside the staging repo rather than inside it: it describes what THIS machine
// has staged, and everything in the tree is committed and shared.
func redactionStatePath(staging string) string {
	if staging == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(staging), ".redaction-state")
}

// redactedLastRun reports whether the previous sync used the current redactor.
// A missing, unreadable or older marker forces one restage of conversation text.
func redactedLastRun(staging string) bool {
	p := redactionStatePath(staging)
	if p == "" {
		return false
	}
	b, err := os.ReadFile(p)
	return err == nil && strings.TrimSpace(string(b)) == redactionVersion
}

// noteRedactionSetting records the setting this sync ran with. Best-effort: a
// marker that cannot be written costs a needless restage next time, which is
// the harmless direction.
func noteRedactionSetting(staging string, on bool) {
	p := redactionStatePath(staging)
	if p == "" {
		return
	}
	v := []byte("0\n")
	if on {
		v = []byte(redactionVersion + "\n")
	}
	_ = os.WriteFile(p, v, 0o644)
}

// sourceLoc resolves where a root's files are read from: the explicit override if
// given (verbatim), else the machine-resolved root location.
// cliSourceLoc resolves the CLI root, which several post-passes need by id.
func cliSourceLoc(opts Options) (string, pathmap.Status) {
	if loc, ok := opts.SourceOverride["cli"]; ok {
		return loc, pathmap.StatusResolved
	}
	return opts.Config.RootLocation("cli", opts.Machine)
}

func sourceLoc(opts Options, r config.Root) (string, pathmap.Status) {
	if loc, ok := opts.SourceOverride[r.ID]; ok {
		return loc, pathmap.StatusResolved
	}
	return r.ResolveOn(opts.Machine)
}

// keepOnly returns the top-level keys to retain for a file that's mostly volatile,
// or nil to keep the whole document. The Desktop config.json is rewritten
// constantly with rotating caches and OAuth token blobs (which is what tripped the
// redaction wire before this filter existed), so it is reduced to the few keys
// that are both stable and portable.
//
// Keep the list conservative — everything omitted is dropped, so a wrong entry
// costs sync coverage, never safety. `preferences` is a nested object Desktop has
// used for settings; `locale` and `userThemeMode` are the flat keys it uses now.
// Deliberately NOT kept: `lastKnownAccountUuid` (identity — syncing it would
// re-point another machine's Desktop at this account), `updaterLastSeenVersion`
// and `first_launch_at` (machine state), and every `oauth:*`/`dxt:*` key (secret
// or cache). Note Desktop's real keys are flat and colon-namespaced
// ("oauth:tokenCache"), not nested.
func keepOnly(rootID, rel string) []string {
	if allowlist.DesktopRoot(rootID) && desktopRel(rootID, rel) == "config.json" {
		return config.DesktopConfigKeepKeys()
	}
	return nil
}

// applyKeepFilter prunes a parsed JSON object to keepOnly's allowed top-level keys.
func applyKeepFilter(rootID, rel string, v any) any {
	keep := keepOnly(rootID, rel)
	if keep == nil {
		return v
	}
	m, ok := v.(map[string]any)
	if !ok {
		return v
	}
	out := make(map[string]any, len(keep))
	for _, k := range keep {
		if val, present := m[k]; present {
			out[k] = val
		}
	}
	return out
}

// scanNonJSON checks the entire staged stream and fails closed on read errors.
//
// An unreadable file is marked File as well, though it is not credential
// material and the summary will call it such. The alternative reads worse: the
// other category is "a value inside a file", and a file nobody could open is
// certainly not that. What the two categories are really steering you toward is
// the remedy, and the remedy here is the file — the Kind is recorded as
// "unreadable" for anyone who looks past the sentence.
func scanNonJSON(srcPath, rel string) *redact.Finding {
	f, err := transcript.Open(srcPath)
	if err != nil {
		return &redact.Finding{Path: rel, Kind: redact.KindUnreadable, File: true}
	}
	defer f.Close()
	found, err := redact.ScanReader(rel, f)
	if err != nil {
		return &redact.Finding{Path: rel, Kind: redact.KindUnreadable, File: true}
	}
	return found
}

func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

func writeFile(path string, data []byte) error { return writeFileMode(path, data, defaultPerm) }

func writeFileMode(path string, data []byte, pm perm) error {
	if err := os.MkdirAll(filepath.Dir(path), pm.dir); err != nil {
		return err
	}
	return os.WriteFile(path, data, pm.file)
}

// linkSlug returns the project slug a CLI-root rel path sits under, or "" when
// the path isn't under projects/.
func linkSlug(rel string) string {
	parts := strings.SplitN(rel, "/", 3)
	if len(parts) >= 2 && parts[0] == "projects" {
		return parts[1]
	}
	return ""
}

func projectIn(m *manifest.Manifest, slug string) bool {
	_, ok := m.Projects[slug]
	return ok
}

// isMemoryRel reports whether a CLI-root rel path is a project memory file
// ("projects/<slug>/memory/…"). Memory is exempt from the retention window: a
// transcript is a dated record and ages out, but a memory is durable state that
// is only rewritten when the fact changes. Aging it by mtime silently stops a
// stable memory from propagating and then deletes it from the staged tree, so a
// fresh restore gets a MEMORY.md index pointing at files it never received.
// They're a few KB each, so there is no size argument for expiring them either.
func isMemoryRel(rel string) bool {
	parts := strings.Split(rel, "/")
	return len(parts) > 3 && parts[0] == "projects" && parts[2] == "memory"
}

// sweepOrphanedTranscripts scrubs staged transcripts the walk never reached.
// visited holds every staged path this run looked at; anything under a root's
// projects/ tree that is missing from it has no live source any more — a deleted
// worktree, an archived project — and will never be restaged, so the staged
// bytes are the only copy there is and the only place a scrub can happen.
//
// Best-effort per file: one that cannot be scrubbed whole (a key block spanning
// lines, content that turns binary) is left exactly as it was, and the tripwire
// still refuses it by name. Silently rewriting half of it would be worse.
func sweepOrphanedTranscripts(stagingDir string, visited map[string]bool) (int, error) {
	swept := 0
	roots, err := os.ReadDir(stagingDir)
	if err != nil {
		return 0, err
	}
	for _, root := range roots {
		if !root.IsDir() {
			continue
		}
		stageRoot := filepath.Join(stagingDir, root.Name())
		projects := filepath.Join(stageRoot, "projects")
		if !dirExists(projects) {
			continue
		}
		err := filepath.WalkDir(projects, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || visited[path] {
				return nil
			}
			rel, rerr := filepath.Rel(stageRoot, path)
			if rerr != nil || !isTranscriptRel(filepath.ToSlash(rel)) {
				return nil
			}
			info, ierr := d.Info()
			if ierr != nil {
				return nil
			}
			// src == dst: redactTranscript reads through an open handle and
			// renames its temp over the top at the end, so the file rewrites
			// itself. The staged mtime is kept, because the incremental check
			// on the next run compares it against a source that no longer
			// exists only for files that DO exist — and churning it here would
			// make every later sync restage this file for no reason.
			hits, rerr := redactTranscript(path, path, info.ModTime())
			if rerr != nil {
				return nil // left as it was; the tripwire will say so
			}
			if len(hits) > 0 {
				swept++
			}
			return nil
		})
		if err != nil {
			return swept, err
		}
	}
	return swept, nil
}

// pruneAgedStagedProjects removes files under projectsDir older than cutoff and
// the directories they empty, enforcing the rolling window on the staged tree.
// Memory files are kept regardless of age (isMemoryRel) and count as content, so
// a project whose transcripts have all aged out keeps its slug for its memory.
// It returns the count removed and the set of top-level slugs that still have
// content (so the manifest can drop the rest). A missing dir is a no-op.
func pruneAgedStagedProjects(projectsDir string, cutoff time.Time) (pruned int, remaining map[string]bool, err error) {
	remaining = map[string]bool{}
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, remaining, nil
		}
		return 0, nil, err
	}
	for _, slugEntry := range entries {
		if !slugEntry.IsDir() {
			continue
		}
		slug := slugEntry.Name()
		slugDir := filepath.Join(projectsDir, slug)
		var kept int
		// remove aged files, deepest first so dirs can be cleaned afterwards
		filepath.WalkDir(slugDir, func(p string, d os.DirEntry, werr error) error {
			if werr != nil {
				return nil
			}
			if d.IsDir() {
				rel, _ := filepath.Rel(projectsDir, p)
				if transcript.IsPartPath("projects/" + filepath.ToSlash(rel)) {
					return filepath.SkipDir
				}
				return nil
			}
			info, e := d.Info()
			if e != nil {
				return nil
			}
			if rel, rerr := filepath.Rel(slugDir, p); rerr == nil && isMemoryRel("projects/"+slug+"/"+filepath.ToSlash(rel)) {
				kept++
				return nil
			}
			if info.ModTime().Before(cutoff) {
				if os.Remove(p) == nil {
					if strings.HasSuffix(p, ".jsonl") {
						_ = os.RemoveAll(p + transcript.Suffix)
					}
					pruned++
				}
			} else {
				kept++
			}
			return nil
		})
		if kept == 0 {
			_ = os.RemoveAll(slugDir)
		} else {
			remaining[slug] = true
			removeEmptyDirs(slugDir)
		}
	}
	return pruned, remaining, nil
}

// removeEmptyDirs removes now-empty subdirectories of root (deepest first).
// reconcileStagedRoot deletes staged files the allowlist no longer permits, and
// returns how many it removed. This is what makes a tightened rule retroactive:
// without it, an exclusion added today only stops NEW files, while everything the
// old rule let through stays in the repo and keeps being pushed and restored.
//
// It judges paths, not existence, so files belonging to other machines (project
// slugs this machine has never seen) are unaffected as long as the allowlist still
// permits them. Retention, which removes allowed-but-aged files, is separate and
// runs on its own.
//
// It judges the allowlist ONLY. An earlier version also retired staged files
// whose live counterpart was a directory, to clean up placeholders left by
// pre-#196 syncs — but nothing on disk distinguishes such a placeholder from a
// legitimately empty file another machine staged, so every narrowing of that
// rule still deleted somebody's data on the next push. It is gone: restore
// already refuses to write through a symlink, which is what made the
// placeholders harmful, so the cleanup bought tidiness at the price of a
// data-loss class.
func reconcileStagedRoot(stageRoot string, l allowlist.List) (removed int, err error) {
	if !dirExists(stageRoot) {
		return 0, nil
	}
	err = filepath.WalkDir(stageRoot, func(p string, d os.DirEntry, werr error) error {
		if werr != nil {
			// A staged tree churning under us is not a reason to fail the sync.
			if os.IsNotExist(werr) {
				return nil
			}
			return werr
		}
		if d.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(stageRoot, p)
		if rerr != nil {
			return nil
		}
		if !l.Match(filepath.ToSlash(rel)) {
			if os.Remove(p) == nil {
				removed++
			}
			return nil
		}
		// A staged FILE whose live counterpart is a DIRECTORY (or a symlink to
		// one) is a category error left by an older sync: the walk now reports
		// directory symlinks as links and never as files, so no future sync will
		// ever refresh or remove this copy, while restore keeps trying to write
		// it back over the live link. Retire it here so the repo can dig itself
		// out. Judged only where the path exists on this machine — staging also
		// carries other machines' files, whose absence here means nothing.
		return nil
	})
	if err != nil {
		return removed, err
	}
	removeEmptyDirs(stageRoot)
	return removed, nil
}

func removeEmptyDirs(root string) {
	var dirs []string
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			dirs = append(dirs, p)
		}
		return nil
	})
	for i := len(dirs) - 1; i >= 0; i-- {
		if dirs[i] != root {
			_ = os.Remove(dirs[i]) // removes only if empty
		}
	}
}

// copyPreserveMtime streams src to dst and stamps dst with src's mtime, so the
// next sync's size+mtime check can skip an unchanged file (incremental sync).
// writeFileMtime stages bytes already in hand, keeping the source mtime so the
// incremental same-size+mtime skip still recognises the copy next sync.
func writeFileMtime(dst string, data []byte, mtime time.Time) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		return err
	}
	return os.Chtimes(dst, mtime, mtime)
}

func copyPreserveMtime(src, dst string, mtime time.Time) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	// Written beside dst and renamed over it, so an interrupted copy leaves
	// the previous staged file where it was rather than a truncated one in
	// its place: the large-file throttle compares against the staged copy,
	// and a truncated copy would pass for a baseline and then be committed.
	out, err := os.CreateTemp(filepath.Dir(dst), "."+filepath.Base(dst)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := out.Name()
	fail := func(err error) error {
		_ = out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		return fail(err)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Chtimes(tmp, mtime, mtime); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// copyTranscriptSnapshot writes large transcripts directly as chunks, publishing
// the index only after all parts are complete. Live sources remain native JSONL.
func copyTranscriptSnapshot(src, dst string, mtime time.Time, chunked bool) error {
	if !chunked {
		return copyPreserveMtime(src, dst, mtime)
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	return transcript.Write(dst, in, mtime)
}

// removeStaged deletes a staged file and, for a chunked transcript, the parts
// that belong to it. Removing the index alone would leave a directory of
// orphaned chunks that nothing references and nothing later cleans up.
// removeStaged is a var for the same reason probeMtimeTick is: the failure path
// matters more than the success one here, and there is no way to make a delete
// fail from outside a run that is itself writing the file it will delete.
var removeStaged = func(staging, rel string) error {
	p := filepath.Join(staging, filepath.FromSlash(rel))
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.RemoveAll(p + transcript.Suffix)
}
