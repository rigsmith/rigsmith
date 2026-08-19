// Package engine orchestrates the sync pipeline: walk each root's allowlist,
// redact secrets, copy into the staging repo, build the project manifest, and run
// the secret tripwire. Pure of git — the caller commits/pushes the staging dir
// via internal/gitrepo. This is where the standalone units (allowlist, redact,
// manifest) compose into the actual operation.
package engine

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rigsmith/rigsmith/core/pathmap"
	"github.com/rigsmith/rigsmith/internal/clauderig/allowlist"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/manifest"
	"github.com/rigsmith/rigsmith/internal/clauderig/redact"
)

// RootResult summarises one root's contribution to a sync.
type RootResult struct {
	ID             string
	Files          int // files written this sync (new or changed)
	Unchanged      int // files already current in staging (incremental skip)
	Redactions     int
	RetentionByAge int      // project transcripts dropped as older than the window
	SkippedFiles   int      // files that vanished/were unreadable mid-sync (live churn)
	Oversize       []string // rel paths dropped for exceeding MaxFileBytes
	Disallowed     int      // staged files removed because the allowlist no longer permits them
	Skipped        bool     // root absent on this machine
}

// Report is the outcome of a sync into the staging dir.
type Report struct {
	Roots            []RootResult
	ManifestProjects int
	RetentionPruned  int              // staged transcript files removed as aged-out
	SidecarsPruned   int              // staged Desktop sidecars removed as orphaned
	Findings         []redact.Finding // non-empty ⇒ Sync returned an error (tripwire)
}

// Options configure a sync.
type Options struct {
	StagingDir    string
	Config        *config.Config
	Machine       config.Machine
	ClaudeVersion string
	// RetentionDays drops project transcripts older than this many days (0 = keep
	// all). Now() is the reference; the cutoff is computed once per sync.
	RetentionDays int
	// MaxFileBytes drops any single file larger than this (<= 0 = no cap). Git
	// hosts reject oversized blobs and take the whole push down with them.
	MaxFileBytes int64
	// SourceOverride maps a root id to an absolute source dir, used verbatim
	// instead of resolving the root location via the machine. The machine still
	// drives path translation (portablize/manifest); this only decouples WHERE the
	// files are read from — symmetric with restore's TargetOverride.
	SourceOverride map[string]string
	// Profiles names the Claude Desktop profiles to sync alongside the configured
	// roots — see profiles.go. Each is walked as its own root, and they follow
	// the Desktop root's enabled flag.
	Profiles []string
}

// Sync materialises the allowlisted, redacted file set for each enabled root into
// StagingDir/<root-id>/…, writes the project manifest, and runs the tripwire over
// the config JSON it wrote. A tripwire hit fails the sync loudly (a secret slipped
// past redaction) — that is the safety property; nothing is pushed in that case.
func Sync(opts Options) (*Report, error) {
	rep := &Report{}
	// Findings from whole files, tracked apart from JSON-value findings because the
	// two need different remedies in the error message.
	credentialFiles := 0
	policy := redact.DefaultPolicy()

	var cutoff time.Time
	if opts.RetentionDays > 0 {
		cutoff = time.Now().AddDate(0, 0, -opts.RetentionDays)
	}

	// Shared-memory symlinks found under the CLI root (worktree slugs linking
	// memory/ to their main project); recorded in the manifest for restore.
	var cliLinks []allowlist.Link

	for _, r := range effectiveRoots(opts.Config, opts.Profiles) {
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

		files, links, err := allowlist.Walk(loc, allowlistFor(r.ID))
		if err != nil {
			return nil, fmt.Errorf("walk %s: %w", r.ID, err)
		}
		if r.ID == "cli" {
			cliLinks = links
		}
		stageRoot := filepath.Join(opts.StagingDir, r.ID)

		for _, rel := range files {
			srcPath := filepath.Join(loc, filepath.FromSlash(rel))
			dstPath := filepath.Join(stageRoot, filepath.FromSlash(rel))
			isJSON := strings.HasSuffix(rel, ".json")

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
			if opts.MaxFileBytes > 0 && info.Size() > opts.MaxFileBytes {
				rr.Oversize = append(rr.Oversize, rel)
				_ = os.Remove(dstPath)
				continue
			}

			// Non-JSON (transcripts, skill files): copy verbatim, but skip if the
			// staging copy is already current (same size+mtime) — incremental sync.
			if !isJSON {
				noteFinding := func(f *redact.Finding) {
					rep.Findings = append(rep.Findings, redact.Finding{
						Path: r.ID + "/" + f.Path, Kind: f.Kind,
					})
					credentialFiles++
				}
				// The name rule needs no content, so it runs on EVERY file, including
				// ones the incremental skip below won't recopy: a credential staged by
				// an earlier sync (or before this check existed) must keep failing until
				// it is dealt with, rather than being hidden forever by that skip.
				if redact.ClassifyName(rel) == redact.NameKeyMaterial {
					noteFinding(&redact.Finding{Path: rel, Kind: "key-material"})
					continue
				}

				unchanged := false
				if d, derr := os.Stat(dstPath); derr == nil && d.Size() == info.Size() && d.ModTime().Equal(info.ModTime()) {
					unchanged = true
				}
				if unchanged {
					// Nothing will be written, so scanning the source separately is safe
					// here — there is no staged copy for it to disagree with.
					if f := scanNonJSON(srcPath, rel, info.Size()); f != nil {
						noteFinding(f)
						continue
					}
					rr.Unchanged++
					continue
				}

				// Scan the EXACT bytes being staged. Reading for the scan and then
				// re-opening to copy would leave a window in which a live ~/.claude
				// replaces a benign file with a credential after it was cleared, staging
				// content that was never scanned. Files past the scan limit have no
				// content rules applied at all (see redact.ScanContentLimit), so for
				// those there is nothing to diverge and a streaming copy is fine.
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
				} else if err := copyPreserveMtime(srcPath, dstPath, info.ModTime()); err != nil {
					if os.IsNotExist(err) {
						rr.SkippedFiles++
						continue
					}
					return nil, err
				}
				rr.Files++
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
			red, paths := redact.Redact(v, policy)
			v, rr.Redactions = red, rr.Redactions+len(paths)
			v, _ = pathmap.PortablizeJSONValues(v, opts.Machine.Folders(), opts.Machine.OS)
			out, e := json.MarshalIndent(v, "", "  ")
			if e != nil {
				rr.SkippedFiles++
				continue
			}
			out = append(out, '\n')
			for _, f := range redact.Scan(v) {
				rep.Findings = append(rep.Findings, redact.Finding{
					Path: r.ID + "/" + rel + ":" + f.Path, Kind: f.Kind,
				})
			}
			if err := writeFile(dstPath, out); err != nil {
				return nil, err
			}
			rr.Files++
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
		disallowed, perr := reconcileStagedRoot(stageRoot, allowlistFor(r.ID))
		if perr != nil {
			return nil, fmt.Errorf("reconcile staged %s: %w", r.ID, perr)
		}
		rr.Disallowed = disallowed

		rep.Roots = append(rep.Roots, rr)
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

	if len(rep.Findings) > 0 {
		// The two halves of the wire need different remedies, so say which one
		// fired: a JSON value means the redactor's key rules missed something, a
		// whole file means it should never have been in the allowlist.
		files := credentialFiles
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
	if isDesktopTree(rootID) && rel == "config.json" {
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

// scanNonJSON runs the non-JSON tripwire over one file, reading only as much of
// it as redact.ScanFile will actually look at — the name rules need no content,
// and anything past the content limit is a transcript-sized file the scan skips
// by design. A file that can't be read is not reported: it is the same churn case
// the copy path already tolerates, and inventing a finding would abort the sync
// over a file that merely vanished.
func scanNonJSON(srcPath, rel string, size int64) *redact.Finding {
	var data []byte
	if size > 0 && size <= int64(redact.ScanContentLimit()) {
		f, err := os.Open(srcPath)
		if err != nil {
			return nil
		}
		data, _ = io.ReadAll(io.LimitReader(f, int64(redact.ScanContentLimit())))
		f.Close()
	}
	if found := redact.ScanFile(rel, data); len(found) > 0 {
		return &found[0]
	}
	return nil
}

func allowlistFor(rootID string) allowlist.List {
	if isDesktopTree(rootID) {
		return allowlist.Desktop()
	}
	return allowlist.CLI()
}

func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
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
			if werr != nil || d.IsDir() {
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
func reconcileStagedRoot(stageRoot string, l allowlist.List) (int, error) {
	if !dirExists(stageRoot) {
		return 0, nil
	}
	removed := 0
	err := filepath.WalkDir(stageRoot, func(p string, d os.DirEntry, werr error) error {
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
		if l.Match(filepath.ToSlash(rel)) {
			return nil
		}
		if os.Remove(p) == nil {
			removed++
		}
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
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chtimes(dst, mtime, mtime)
}
