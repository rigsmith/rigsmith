package engine

import (
	"encoding/json"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/rigsmith/rigsmith/core/pathmap"
	"github.com/rigsmith/rigsmith/internal/agentrig/files"
	"github.com/rigsmith/rigsmith/internal/clauderig/account"
	"github.com/rigsmith/rigsmith/internal/clauderig/adapter"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/desktop"
	"github.com/rigsmith/rigsmith/internal/clauderig/manifest"
	"github.com/rigsmith/rigsmith/internal/clauderig/project"
	"github.com/rigsmith/rigsmith/internal/clauderig/redact"
	"github.com/rigsmith/rigsmith/internal/clauderig/transcript"
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
	// LiveSkipped holds the target-relative transcripts left alone because a
	// running Claude Code session is appending to them.
	LiveSkipped []string
	Skipped     bool
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

// prunableDirs are the authoritative config dirs where "deleted upstream" means
// "remove locally". projects/ is deliberately excluded — it's additive (a machine
// accumulates its own local sessions), so it is never pruned.
var prunableDirs = []string{"skills", "commands", "agents", "plans"}

// RestoreReport is the outcome of a restore.
type RestoreReport struct {
	Roots []RestoreRootResult
	// Unaccounted counts Claude Code processes using this ~/.claude that the
	// session registry does not list. The live-transcript guard identifies what
	// to protect by reading session ids out of that registry, so a process
	// missing from it protects nothing — and looks exactly like no session
	// running at all. Reported so the command layer can say the guard had a
	// blind spot, rather than leaving the restore looking wholly guarded.
	Unaccounted int
}

// LiveSkips lists every transcript skipped because a Claude Code session was
// writing to it, across all roots. The command layer names these: a guard that
// silently drops files reads as "everything restored" when it didn't.
func (r *RestoreReport) LiveSkips() []string {
	var out []string
	for _, root := range r.Roots {
		out = append(out, root.LiveSkipped...)
	}
	return out
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
	if _, err := transcript.Enabled(opts.StagingDir); err != nil {
		return nil, err
	}
	rep := &RestoreReport{}
	for _, r := range adapter.Roots(opts.Config, opts.Profiles) {
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
		pm := permFor(r.ID)
		// Transcripts a live session is mid-write on. Computed against the
		// target, so a --dir restore into a scratch folder finds none — the
		// question is always "is anything writing to the tree I'm about to
		// overwrite", not "is Claude running somewhere".
		live := liveTranscripts(target)
		if n, ok := account.UnaccountedProcesses(target); ok {
			rep.Unaccounted = max(rep.Unaccounted, n)
		}

		restored, err := files.Restore(files.RestoreOptions{
			SourceDir: stageRoot, TargetDir: target, Live: live,
			Plan: func(rel string) (string, bool) {
				if transcript.IsPartPath(rel) {
					return "", true
				}
				if r.ID == "cli" && strings.HasPrefix(rel, "projects/") {
					newRel, srcSlug, did := rewriteProjectRel(rel, slugMap)
					if did {
						rewritten[srcSlug] = true
					}
					return newRel, false
				}
				return rel, false
			},
			Write: func(src, dst, rel, targetRel string) error {
				var err error
				if r.Classify(rel).Transform == adapter.JSON {
					err = restoreJSON(src, dst, opts.Machine.Resolver(), pm)
				} else {
					err = copyFile(src, dst, pm)
				}
				if err == nil && r.Classify(targetRel).Kind == adapter.DesktopCodeSidecar {
					rr.DesktopSessions++
				}
				return err
			},
		})
		if err != nil {
			return nil, err
		}
		rr.Files, rr.LinksKept, rr.Conflicts = restored.Files, restored.LinksKept, restored.Conflicts
		rr.LiveSkipped = restored.LiveSkipped
		rr.SlugsRewritten = len(rewritten)
		if r.ID == "cli" && opts.Manifest != nil {
			rr.Links = restoreLinks(target, opts.Manifest.Links, slugMap)
		}

		if opts.Prune && r.ID == "cli" {
			pruned, err := pruneConfigDirs(target, restored.Written, restored.Protected)
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
	if len(manifestLinks) == 0 {
		return 0
	}
	root, err := os.OpenRoot(target)
	if err != nil {
		return 0
	}
	defer root.Close()
	links := files.LinkCache{}
	n := 0
	for rel, tgtRel := range manifestLinks {
		// Validate before rewriting too: a malformed source slug must not be
		// made to look safe by a mapping. Backup metadata is not trusted input.
		if !validRestoreLinkPath(rel) || !validRestoreLinkPath(tgtRel) {
			continue
		}
		rel, _, _ = rewriteProjectRel(rel, slugMap)
		tgtRel, _, _ = rewriteProjectRel(tgtRel, slugMap)
		if !validRestoreLinkPath(rel) || !validRestoreLinkPath(tgtRel) {
			continue
		}
		linkName, targetName := filepath.FromSlash(rel), filepath.FromSlash(tgtRel)
		linkPath := filepath.Join(target, linkName)
		if info, err := root.Stat(targetName); err != nil || !info.IsDir() {
			continue // target absent on this machine — nothing to point at
		}
		if _, err := root.Lstat(linkName); !os.IsNotExist(err) {
			continue
		}
		// The same ancestor rule the write loop applies. Checking only the leaf
		// lets MkdirAll and Symlink follow a linked ancestor and create the link
		// OUTSIDE the restore target — writing into a directory the user never
		// pointed restore at.
		if links.UnderSymlink(target, linkPath) {
			continue
		}
		// Root keeps creation inside the chosen directory even if a parent
		// changes after the ancestor check. Relative targets also let Windows
		// Root.Symlink recognize this as a directory link.
		linkTarget, err := filepath.Rel(filepath.Dir(linkName), targetName)
		if err != nil {
			continue
		}
		if err := root.MkdirAll(filepath.Dir(linkName), 0o755); err != nil {
			continue
		}
		if err := root.Symlink(linkTarget, linkName); err == nil {
			n++
		}
	}
	return n
}

// Manifest endpoints use portable slash-relative names, never absolute paths,
// parent traversal, drive/UNC paths or alternate Windows stream names.
func validRestoreLinkPath(p string) bool {
	return p != "." && fs.ValidPath(p) && !strings.ContainsAny(p, "\\:\x00") && filepath.IsLocal(filepath.FromSlash(p))
}

// liveTranscripts returns the target-relative transcript paths that running
// Claude Code sessions are appending to, as a set of slash-relative paths like
// "projects/-Users-john-Git-foo/<session-id>.jsonl".
//
// This is restore's half of the guard `account switch` already has. Before it,
// restore copied every staged file unconditionally — no newer-than check, no
// skip-if-exists — so a session active since the last sync had its transcript
// rolled back over the top, losing the conversation since that sync.
//
// The match is per-session, not per-project: only the file actually in flight is
// protected, so other sessions in the same project still restore normally.
// claudeHome is the CLI root being written to; a home with no sessions/ (a fresh
// machine, or a --dir scratch target) yields an empty set.
//
// A protected path need not exist yet. A session that has registered but not
// flushed its first turn is precisely the one that must not have a stale copy
// dropped underneath it, or it appends its next turns onto resurrected content.
// Checked against a real ~/.claude: of 17 live sessions, 3 had no transcript on
// disk yet.
func liveTranscripts(claudeHome string) map[string]bool {
	live := map[string]bool{}
	for _, inst := range account.RunningInstances(claudeHome) {
		// IDE bridge locks have no transcript of their own.
		if inst.SessionID == "" || inst.Cwd == "" {
			continue
		}
		live[path.Join("projects", project.Flatten(inst.Cwd), inst.SessionID+".jsonl")] = true
	}
	return live
}

// pruneConfigDirs removes files under the authoritative config dirs that aren't in
// the restored set (deleted upstream). written holds the slash-relative paths just
// written. projects/ is never visited.
func pruneConfigDirs(target string, written, protected map[string]bool) (int, error) {
	return files.Prune(target, prunableDirs, written, protected)
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
	v, _ = desktop.ResolveJSONPaths(v, resolver)
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

func copyFile(src, dst string, pm perm) error {
	if strings.HasSuffix(src, ".jsonl") {
		if err := os.MkdirAll(filepath.Dir(dst), pm.Dir); err != nil {
			return err
		}
		return transcript.Materialize(src, dst, pm.File)
	}
	return files.Copy(src, dst, pm)
}

func copyBytes(dst string, data []byte, pm perm) error { return writeFileMode(dst, data, pm) }
