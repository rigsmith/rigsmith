// Package mergepolicy resolves conflicts in clauderig's sync staging repo without
// asking a human.
//
// The staging repo is not a codebase: every file in it is one machine's snapshot
// of its own Claude Code setup, and two machines syncing the same file are not
// two authors disagreeing about one text. That makes every conflict here
// mechanically decidable, which matters because the merge usually happens where
// nobody can answer a prompt — a SessionStart hook, an agent, CI. Left unresolved
// it is worse than a stalled sync: a staging repo abandoned mid-merge wedges every
// later pull with "unmerged files" until someone finds it by hand.
//
// Three policies cover the tree:
//
//   - clauderig's own metadata (the manifest and the device registry) is a UNION
//     across machines by definition — restore on a third machine needs every
//     machine's projects, not the last writer's.
//   - append-shaped text (transcripts, memory notes) takes both sides' lines:
//     each machine added its own and neither is a correction of the other.
//   - everything else is machine-local state, where the later snapshot is simply
//     the later truth.
package mergepolicy

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/internal/clauderig/devices"
	"github.com/rigsmith/rigsmith/internal/clauderig/manifest"
)

// Policy names how a path was resolved, for the report sync prints.
type Policy string

const (
	// PolicyUnionMeta merges clauderig's own JSON metadata key-by-key.
	PolicyUnionMeta Policy = "union"
	// PolicyUnionText keeps both sides' lines in append-shaped text.
	PolicyUnionText Policy = "both"
	// PolicyNewest takes whichever side was committed later.
	PolicyNewest Policy = "newest"
	// PolicyKeep keeps the surviving side of a delete-vs-edit conflict. Named
	// apart from PolicyNewest because no comparison happened: one side simply
	// still exists, and reporting that as "newest" would describe a decision the
	// code never made.
	PolicyKeep Policy = "kept"
)

// Resolution records one resolved path and why.
type Resolution struct {
	Path   string
	Policy Policy
	Note   string
}

// Report is the outcome of a Resolve pass.
type Report struct {
	Resolved   []Resolution
	Unresolved []string
}

// Resolve applies the policies to every conflicted path in an in-progress merge,
// staging what it settles. It does NOT commit — the caller decides that, because
// only the caller knows whether anything is left for a human. Paths it cannot
// decide are returned in Unresolved and left conflicted.
func Resolve(ctx context.Context, repo *gitrepo.Repo) (Report, error) {
	var rep Report
	paths, err := repo.Conflicts(ctx)
	if err != nil {
		return rep, err
	}
	for _, p := range paths {
		res, ok := resolveOne(ctx, repo, p)
		if !ok {
			rep.Unresolved = append(rep.Unresolved, p)
			continue
		}
		rep.Resolved = append(rep.Resolved, res)
	}
	return rep, nil
}

func resolveOne(ctx context.Context, repo *gitrepo.Repo, p string) (Resolution, bool) {
	switch {
	case p == manifest.FileName:
		if note, ok := unionManifest(ctx, repo, p); ok {
			return Resolution{Path: p, Policy: PolicyUnionMeta, Note: note}, true
		}
	case p == devices.FileName:
		if note, ok := unionDevices(ctx, repo, p); ok {
			return Resolution{Path: p, Policy: PolicyUnionMeta, Note: note}, true
		}
	case isAppendText(p):
		if content, ok := repo.UnionMerge(ctx, p); ok {
			content, dropped := dedupRecords(p, content)
			if err := repo.ResolveWith(ctx, p, content); err == nil {
				note := "kept both machines' lines"
				if dropped > 0 {
					note += fmt.Sprintf(", dropped %d repeated record(s)", dropped)
				}
				return Resolution{Path: p, Policy: PolicyUnionText, Note: note}, true
			}
		}
	}
	return newest(ctx, repo, p)
}

// isAppendText reports whether a path is one of the grow-by-appending files where
// keeping both sides is right.
//
// Two different rules, because "text" is not the property that matters:
//
//   - Session transcripts (.jsonl) are literally append-only, wherever they sit.
//   - Prose is unioned ONLY inside a memory/ directory. That is where a machine
//     adds what it learned, so two sides are additions to a shared note. Every
//     other synced document — CLAUDE.md, skills, plans, commands, agents — is a
//     document someone EDITS, and concatenating two revisions of one would
//     produce a file that contradicts itself while reporting success. Those take
//     the newest-snapshot fallback like any other whole-file conflict.
func isAppendText(p string) bool {
	if strings.EqualFold(path.Ext(p), ".jsonl") {
		return true
	}
	switch strings.ToLower(path.Ext(p)) {
	case ".md", ".markdown", ".txt":
		return isMemoryPath(p)
	}
	return false
}

// isMemoryPath reports whether a staged path lies inside a memory/ directory —
// projects/<slug>/memory/… in the CLI root, and the same shape under the repo's
// cli/ prefix.
func isMemoryPath(p string) bool {
	for _, seg := range strings.Split(path.Dir(p), "/") {
		if seg == "memory" {
			return true
		}
	}
	return false
}

// dedupRecords drops repeated records from a unioned TRANSCRIPT, keeping the
// first occurrence of each uuid and every line that has none.
//
// A transcript conflict only arises when the same session was continued
// independently on both machines, and unioning two divergent tails can then emit
// the same record twice. That matters more than tidiness: records carry
// uuid/parentUuid links and `claude --resume` reads them in order, so a duplicated
// uuid is a fork in the chain rather than a cosmetic repeat.
//
// Only .jsonl. Markdown is left exactly as unioned — a repeated line there is
// usually legitimate (blank lines, list markers, code fences), and dropping one
// would edit a note rather than merge it.
func dedupRecords(p string, content []byte) ([]byte, int) {
	if !strings.EqualFold(path.Ext(p), ".jsonl") {
		return content, 0
	}
	lines := strings.Split(string(content), "\n")
	seen := make(map[string]bool, len(lines))
	out := make([]string, 0, len(lines))
	dropped := 0
	for _, line := range lines {
		var rec struct {
			UUID string `json:"uuid"`
		}
		if strings.TrimSpace(line) != "" && json.Unmarshal([]byte(line), &rec) == nil && rec.UUID != "" {
			if seen[rec.UUID] {
				dropped++
				continue
			}
			seen[rec.UUID] = true
		}
		out = append(out, line)
	}
	if dropped == 0 {
		return content, 0
	}
	return []byte(strings.Join(out, "\n")), dropped
}

// newest takes the side whose snapshot was committed later. It is the fallback for
// everything without a smarter rule — machine-local caches, editor state, settings
// — where merging two snapshots has no meaning and the later one simply wins. When
// one side deleted the file, the surviving side is kept: sync re-prunes on the next
// pass if the deletion was retention doing its job, and a wrongly-kept file is far
// cheaper than a wrongly-dropped one.
func newest(ctx context.Context, repo *gitrepo.Repo, p string) (Resolution, bool) {
	ours, hasOurs := repo.ConflictStage(ctx, p, 2)
	theirs, hasTheirs := repo.ConflictStage(ctx, p, 3)
	switch {
	case hasOurs && !hasTheirs:
		return keepAs(ctx, repo, p, ours, PolicyKeep, "kept this machine's copy (other side deleted it)")
	case hasTheirs && !hasOurs:
		return keepAs(ctx, repo, p, theirs, PolicyKeep, "kept the incoming copy (this side deleted it)")
	case !hasOurs && !hasTheirs:
		return Resolution{}, false
	}
	ourT, okOur := repo.SideCommitTime(ctx, "HEAD", p)
	theirT, okTheir := repo.SideCommitTime(ctx, "MERGE_HEAD", p)
	if okOur && okTheir && ourT.After(theirT) {
		return keep(ctx, repo, p, ours, "this machine's snapshot is newer")
	}
	// Ties and unknowns go to the incoming side: it is the copy the remote already
	// agrees on, so choosing it converges every machine on one answer instead of
	// each preferring its own and re-conflicting forever.
	return keep(ctx, repo, p, theirs, "incoming snapshot is newer or equal")
}

func keep(ctx context.Context, repo *gitrepo.Repo, p string, content []byte, note string) (Resolution, bool) {
	return keepAs(ctx, repo, p, content, PolicyNewest, note)
}

func keepAs(ctx context.Context, repo *gitrepo.Repo, p string, content []byte, pol Policy, note string) (Resolution, bool) {
	if err := repo.ResolveWith(ctx, p, content); err != nil {
		return Resolution{}, false
	}
	return Resolution{Path: p, Policy: pol, Note: note}, true
}

// unionManifest merges the two sides' project and link maps. Restore reads this to
// map a slug back to a real directory, so a machine missing from it is a machine
// whose history cannot be restored anywhere — the union is the only safe answer.
//
// Where both sides carry the same slug, OURS wins — deliberately not the incoming
// tie-break the whole-file policy uses. A slug's entry is a statement about where
// that project lives on the machine writing it, and this machine is the authority
// on its own paths. The asymmetry cannot drift: the next sync rebuilds the whole
// manifest from this machine's projects dir anyway.
func unionManifest(ctx context.Context, repo *gitrepo.Repo, p string) (string, bool) {
	var ours, theirs manifest.Manifest
	if !decodeSides(ctx, repo, p, &ours, &theirs) {
		return "", false
	}
	merged := theirs
	if merged.Projects == nil {
		merged.Projects = map[string]manifest.Project{}
	}
	for k, v := range ours.Projects {
		merged.Projects[k] = v
	}
	if len(ours.Links) > 0 && merged.Links == nil {
		merged.Links = map[string]string{}
	}
	for k, v := range ours.Links {
		merged.Links[k] = v
	}
	b, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return "", false
	}
	if err := repo.ResolveWith(ctx, p, append(b, '\n')); err != nil {
		return "", false
	}
	return fmt.Sprintf("%d projects from both machines", len(merged.Projects)), true
}

// unionDevices merges the registry by machine name, keeping each machine's latest
// sync time — one machine's push must never erase another machine's entry.
func unionDevices(ctx context.Context, repo *gitrepo.Repo, p string) (string, bool) {
	var ours, theirs devices.Registry
	if !decodeSides(ctx, repo, p, &ours, &theirs) {
		return "", false
	}
	merged := theirs
	if merged.Devices == nil {
		merged.Devices = map[string]devices.Device{}
	}
	for name, d := range ours.Devices {
		if cur, ok := merged.Devices[name]; !ok || d.LastSync.After(cur.LastSync) {
			merged.Devices[name] = d
		}
	}
	b, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return "", false
	}
	if err := repo.ResolveWith(ctx, p, append(b, '\n')); err != nil {
		return "", false
	}
	return fmt.Sprintf("%d device(s)", len(merged.Devices)), true
}

func decodeSides(ctx context.Context, repo *gitrepo.Repo, p string, ours, theirs any) bool {
	ob, okO := repo.ConflictStage(ctx, p, 2)
	tb, okT := repo.ConflictStage(ctx, p, 3)
	if !okO || !okT {
		return false
	}
	return json.Unmarshal(ob, ours) == nil && json.Unmarshal(tb, theirs) == nil
}
