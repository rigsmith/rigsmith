package engine

import (
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/rigsmith/rigsmith/core/pathmap"
	"github.com/rigsmith/rigsmith/internal/clauderig/allowlist"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/manifest"
	"github.com/rigsmith/rigsmith/internal/clauderig/project"
	"github.com/rigsmith/rigsmith/internal/clauderig/redact"
)

// RestoreRootResult summarises one root's restore.
type RestoreRootResult struct {
	ID             string
	Files          int
	SlugsRewritten int
	Links          int // shared-memory symlinks recreated from the manifest
	// LinksKept counts staged files NOT written because a symlink at or above
	// the destination holds that path. Reported in the summary rather than
	// folded into Files: "✓ restored" over a silently unwritten file is the
	// kind of quiet success this whole path exists to avoid.
	LinksKept int
	// Conflicts counts staged files skipped because a DIRECTORY holds that
	// destination — a path that is a file on one machine and a directory here.
	// Writing one would abort the whole restore with EISDIR.
	Conflicts int
	Pruned    int // files removed as deleted-upstream (--prune)
	// DesktopSessions counts Claude Desktop Code-session sidecars written this
	// restore (claude-code-sessions/**/local_*.json). Desktop only rebuilds its
	// Code-tab list from these on startup, so the command layer uses this to nudge
	// a restart when new sessions land.
	DesktopSessions int
	Skipped         bool
}

// DesktopSessions totals the Desktop Code-session sidecars written across all
// roots this restore — the count behind the "restart Desktop" nudge.
func (r *RestoreReport) DesktopSessions() int {
	n := 0
	for _, rr := range r.Roots {
		n += rr.DesktopSessions
	}
	return n
}

// isDesktopSessionSidecar reports whether a restored (slash) rel path is a
// Desktop Code-session sidecar:
// claude-code-sessions/<accountUuid>/<organizationUuid>/local_<id>.json. Those
// two uuids are the account's, straight from ~/.claude.json's oauthAccount — so
// this tree is already partitioned per account, which is what devices.Account
// records for the CLI side, where nothing else does.
// The local_<id>.json shape is matched on the basename so a directory like
// claude-code-sessions/org/local_cache/other.json isn't miscounted as a session.
func isDesktopSessionSidecar(rel string) bool {
	if !strings.HasPrefix(rel, "claude-code-sessions/") {
		return false
	}
	base := path.Base(rel)
	return strings.HasPrefix(base, "local_") && strings.HasSuffix(base, ".json")
}

// prunableDirs are the authoritative config dirs where "deleted upstream" means
// "remove locally". projects/ is deliberately excluded — it's additive (a machine
// accumulates its own local sessions), so it is never pruned.
var prunableDirs = []string{"skills", "commands", "agents", "plans"}

// RestoreReport is the outcome of a restore.
type RestoreReport struct {
	Roots []RestoreRootResult
}

// RestoreOptions configure a restore.
type RestoreOptions struct {
	StagingDir string
	Config     *config.Config
	Machine    config.Machine
	Manifest   *manifest.Manifest
	// TargetOverride maps a root id to an absolute target dir, overriding its
	// resolved location (used by `restore --dir` to write into a test folder).
	TargetOverride map[string]string
	// OverriddenOnly restores only roots present in TargetOverride (so a --dir
	// restore touches the test folder and nothing else).
	OverriddenOnly bool
	// Prune removes files under the authoritative config dirs (prunableDirs) that
	// aren't in the synced set — so a skill deleted upstream is deleted locally.
	// Never touches projects/ (additive).
	Prune bool
	// Profiles names the Claude Desktop profiles to restore alongside the
	// configured roots — see profiles.go. Read from the STAGING tree rather than
	// from local state (engine.StagedProfileNames), so a machine that has never
	// run `clauderig desktop` still gets every profile back.
	Profiles []string
}

// Restore writes the staged file set back to this machine's roots, rewriting CLI
// project slugs for this machine's path layout (via the manifest) and merging
// redacted config so the machine's real secrets are never clobbered by a
// placeholder. Caller handles target-non-empty safety (backup/abort) first.
func Restore(opts RestoreOptions) (*RestoreReport, error) {
	rep := &RestoreReport{}
	for _, r := range EffectiveRoots(opts.Config, opts.Profiles) {
		if !r.Enabled {
			continue
		}
		rr := RestoreRootResult{ID: r.ID}
		override, hasOverride := opts.TargetOverride[r.ID]
		if opts.OverriddenOnly && !hasOverride {
			continue // --dir mode: only the overridden root(s)
		}
		target, st := override, pathmap.StatusResolved
		if !hasOverride {
			target, st = r.ResolveOn(opts.Machine)
		}
		stageRoot := filepath.Join(opts.StagingDir, r.ID)
		if st != pathmap.StatusResolved || !dirExists(stageRoot) {
			rr.Skipped = true
			rep.Roots = append(rep.Roots, rr)
			continue
		}

		var slugMap map[string]string
		if r.ID == "cli" && opts.Manifest != nil {
			slugMap = buildSlugMap(opts.Manifest, opts.Machine)
		}
		rewritten := map[string]bool{}
		written := map[string]bool{}
		// protected holds destinations restore refused to write. Their whole
		// subtree is off-limits to --prune: restore knows nothing about what is
		// inside them, so "not in the synced set" is not evidence of anything.
		protected := map[string]bool{}
		links := linkCache{}
		pm := permFor(r.ID)

		files, err := listFiles(stageRoot)
		if err != nil {
			return nil, err
		}
		for _, rel := range files {
			targetRel := rel
			if r.ID == "cli" && strings.HasPrefix(rel, "projects/") {
				newRel, srcSlug, did := rewriteProjectRel(rel, slugMap)
				targetRel = newRel
				if did {
					rewritten[srcSlug] = true
				}
			}
			src := filepath.Join(stageRoot, filepath.FromSlash(rel))
			dst := filepath.Join(target, filepath.FromSlash(targetRel))

			// A symlink at or above dst is this machine's own state — nearly always
			// one of the shared-memory links restoreLinks recreates. Every write
			// below follows a symlink, so restoring a staged file over one would
			// silently clobber the link's target, or fail outright with EISDIR when
			// the link points at a directory. Leave it alone (and count it as
			// written so --prune doesn't collect it).
			//
			// Ancestors matter as much as the leaf: another machine holding this
			// project as a real directory stages projects/<slug>/memory/MEMORY.md,
			// and writing that descendant here follows the linked memory/ straight
			// into the canonical project.
			if isSymlink(dst) || links.underSymlink(target, dst) {
				written[targetRel] = true
				rr.LinksKept++
				continue
			}

			// A real DIRECTORY at dst cannot be written either: copyFile and
			// restoreJSON both open it, and the open fails with EISDIR — taking
			// the entire restore down over one path.
			//
			// This is the same abort the symlink guard was written to stop,
			// reaching here by a different route. Sync used to delete the staged
			// file in this situation, but that deleted other machines' data too,
			// so staging now keeps whatever it has and the resilience has to
			// live where the write happens. Reported, not silent — a path this
			// machine cannot accept is worth saying out loud.
			if conflictAt(target, dst) {
				written[targetRel] = true
				// The destination is a directory this restore will not write
				// into, so everything ALREADY inside it must survive --prune.
				// Recording only targetRel marked the collision itself as
				// written and left the directory's real contents looking absent
				// from the synced set — so prune deleted the user's files under
				// a path restore had just declined to touch.
				protected[targetRel] = true
				rr.Conflicts++
				continue
			}

			if strings.HasSuffix(rel, ".json") {
				if err := restoreJSON(src, dst, opts.Machine.Resolver(), pm); err != nil {
					return nil, err
				}
			} else if err := copyFile(src, dst, pm); err != nil {
				return nil, err
			}
			written[targetRel] = true
			rr.Files++
			if allowlist.DesktopRoot(r.ID) && isDesktopSessionSidecar(desktopRel(r.ID, targetRel)) {
				rr.DesktopSessions++
			}
		}
		rr.SlugsRewritten = len(rewritten)
		if r.ID == "cli" && opts.Manifest != nil {
			rr.Links = restoreLinks(target, opts.Manifest.Links, slugMap)
		}

		if opts.Prune && r.ID == "cli" {
			pruned, err := pruneConfigDirs(target, written, protected)
			if err != nil {
				return nil, err
			}
			rr.Pruned = pruned
		}
		rep.Roots = append(rep.Roots, rr)
	}
	return rep, nil
}

// restoreLinks recreates the shared-memory symlinks the manifest records,
// rewriting both endpoints through this machine's slug map. A link is created
// only when its target directory exists (was restored or already lived here) and
// nothing occupies the link path — an existing file, dir, or link is the
// machine's own state and is left alone. A failed creation (e.g. symlinks
// unavailable on the platform) skips that link, never the restore.
func restoreLinks(target string, manifestLinks map[string]string, slugMap map[string]string) int {
	links := linkCache{}
	n := 0
	for rel, tgtRel := range manifestLinks {
		rel, _, _ = rewriteProjectRel(rel, slugMap)
		tgtRel, _, _ = rewriteProjectRel(tgtRel, slugMap)
		linkPath := filepath.Join(target, filepath.FromSlash(rel))
		tgtPath := filepath.Join(target, filepath.FromSlash(tgtRel))
		if info, err := os.Stat(tgtPath); err != nil || !info.IsDir() {
			continue // target absent on this machine — nothing to point at
		}
		if _, err := os.Lstat(linkPath); err == nil {
			continue
		}
		// The same ancestor rule the write loop applies. Checking only the leaf
		// lets MkdirAll and Symlink follow a linked ancestor and create the link
		// OUTSIDE the restore target — writing into a directory the user never
		// pointed restore at.
		if links.underSymlink(target, linkPath) {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(linkPath), 0o755); err != nil {
			continue
		}
		if err := os.Symlink(tgtPath, linkPath); err == nil {
			n++
		}
	}
	return n
}

// pruneConfigDirs removes files under the authoritative config dirs that aren't in
// the restored set (deleted upstream). written holds the slash-relative paths just
// written. projects/ is never visited.
func pruneConfigDirs(target string, written, protected map[string]bool) (int, error) {
	pruned := 0
	for _, dir := range prunableDirs {
		base := filepath.Join(target, dir)
		if !dirExists(base) {
			continue
		}
		err := filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			// Never delete a symlink. It is the machine's own state — the same
			// rule the restore loop applies when it declines to write through
			// one — and a link is recorded in `written` only under the
			// DESCENDANT path that was skipped, so judging the link itself by
			// that map would collect it every time.
			if d.Type()&fs.ModeSymlink != 0 {
				return nil
			}
			rel, rerr := filepath.Rel(target, p)
			if rerr != nil {
				return rerr
			}
			relSlash := filepath.ToSlash(rel)
			if underProtected(relSlash, protected) {
				return nil
			}
			if !written[relSlash] {
				if err := os.Remove(p); err != nil {
					return err
				}
				pruned++
			}
			return nil
		})
		if err != nil {
			return pruned, err
		}
	}
	return pruned, nil
}

// buildSlugMap maps each source slug to this machine's slug, via the manifest's
// portable template resolved for this machine. A project with no template (cwd not
// under a known folder) or an unresolvable one keeps its source slug.
func buildSlugMap(m *manifest.Manifest, mc config.Machine) map[string]string {
	out := make(map[string]string, len(m.Projects))
	res := mc.Resolver()
	for srcSlug, p := range m.Projects {
		if p.Template == "" {
			out[srcSlug] = srcSlug
			continue
		}
		ns, _, st := project.RewriteFromTemplate(p.Template, res)
		if st == pathmap.StatusResolved {
			out[srcSlug] = ns
		} else {
			out[srcSlug] = srcSlug
		}
	}
	return out
}

// rewriteProjectRel maps "projects/<srcSlug>/<rest>" to the target slug. It
// returns the new rel, the source slug, and whether the slug actually changed.
func rewriteProjectRel(rel string, slugMap map[string]string) (newRel, srcSlug string, rewrote bool) {
	parts := strings.SplitN(rel, "/", 3)
	if len(parts) < 2 {
		return rel, "", false
	}
	srcSlug = parts[1]
	tgt, ok := slugMap[srcSlug]
	if !ok || tgt == srcSlug {
		return rel, srcSlug, false
	}
	newRel = "projects/" + tgt
	if len(parts) == 3 {
		newRel += "/" + parts[2]
	}
	return newRel, srcSlug, true
}

// restoreJSON writes a synced JSON file to dst, resolving portable path values to
// this machine and merging onto the local file so the machine's real secrets
// survive (any synced JSON may carry redaction placeholders). Unparseable JSON
// falls back to a raw copy.
func restoreJSON(src, dst string, resolver *pathmap.Resolver, pm perm) error {
	synced, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	var v any
	if err := json.Unmarshal(synced, &v); err != nil {
		return copyBytes(dst, synced, pm) // not JSON after all — copy raw
	}
	v, _ = pathmap.ResolveJSONValues(v, resolver)
	resolved, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return copyBytes(dst, synced, pm)
	}
	resolved = append(resolved, '\n')

	local, _ := os.ReadFile(dst) // absent on a fresh machine
	merged, err := redact.MergeBytes(resolved, local)
	if err != nil {
		return writeFileMode(dst, resolved, pm)
	}
	return writeFileMode(dst, merged, pm)
}

func listFiles(root string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return rerr
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	return out, err
}

// linkCache remembers which destination directories sit on or under a symlink,
// so the ancestor walk costs one Lstat per directory across the whole restore
// rather than one per path component per file.
type linkCache map[string]bool

// underSymlink reports whether any ancestor of dst, up to and excluding root, is
// a symlink. root itself is never judged: the target directory is where the user
// pointed restore, and following it is the whole intent.
func (c linkCache) underSymlink(root, dst string) bool {
	dir := filepath.Dir(dst)
	if v, ok := c[dir]; ok {
		return v
	}
	rel, err := filepath.Rel(root, dir)
	// Only ".." itself, or a path BELOW it, is outside the root. A bare prefix
	// test would also catch a real directory named "..memory" — Rel returns that
	// name unchanged — and skip the very symlink check this exists to perform.
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false // at or outside the root — nothing left to walk
	}
	res := isSymlink(dir) || c.underSymlink(root, dir)
	c[dir] = res
	return res
}

// conflictAt reports whether something at or above dst makes it unwriteable:
// a directory occupying dst itself, or a regular FILE occupying one of its
// ancestors.
//
// Both end the same way if written through — EISDIR from the open, or ENOTDIR
// from MkdirAll — and both would take the whole restore down over one path. The
// ancestor case is easy to miss because Lstat(dst) returns ENOTDIR rather than
// describing dst, so a check that only inspects dst never sees it.
func conflictAt(root, dst string) bool {
	if fi, err := os.Lstat(dst); err == nil && fi.IsDir() {
		return true
	}
	for dir := filepath.Dir(dst); ; dir = filepath.Dir(dir) {
		rel, err := filepath.Rel(root, dir)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return false
		}
		if fi, lerr := os.Lstat(dir); lerr == nil && !fi.IsDir() {
			return true // a file where a directory has to be
		}
	}
}

// underProtected reports whether rel sits at or beneath a destination restore
// declined to write.
func underProtected(rel string, protected map[string]bool) bool {
	for p := range protected {
		if rel == p || strings.HasPrefix(rel, p+"/") {
			return true
		}
	}
	return false
}

// isSymlink reports whether p is a symlink, without following it. A missing path
// is not a symlink, so a fresh machine takes the ordinary write path.
func isSymlink(p string) bool {
	fi, err := os.Lstat(p)
	return err == nil && fi.Mode()&fs.ModeSymlink != 0
}

func copyFile(src, dst string, pm perm) error {
	if err := os.MkdirAll(filepath.Dir(dst), pm.dir); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, pm.file)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func copyBytes(dst string, data []byte, pm perm) error { return writeFileMode(dst, data, pm) }
