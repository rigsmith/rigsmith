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
			if err := repo.ResolveWith(ctx, p, content); err == nil {
				return Resolution{Path: p, Policy: PolicyUnionText, Note: "kept both machines' lines"}, true
			}
		}
	}
	return newest(ctx, repo, p)
}

// isAppendText reports whether a path is one of the grow-by-appending files where
// keeping both sides is right. Session transcripts are literally append-only, and
// memory notes are edited the same way — a machine adds what it learned, so the
// two sides are additions to a shared note rather than rival versions of it.
func isAppendText(p string) bool {
	switch strings.ToLower(path.Ext(p)) {
	case ".jsonl":
		return true
	case ".md", ".markdown", ".txt":
		return true
	}
	return false
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
		return keep(ctx, repo, p, ours, "kept this machine's copy (other side deleted it)")
	case hasTheirs && !hasOurs:
		return keep(ctx, repo, p, theirs, "kept the incoming copy (this side deleted it)")
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
	if err := repo.ResolveWith(ctx, p, content); err != nil {
		return Resolution{}, false
	}
	return Resolution{Path: p, Policy: PolicyNewest, Note: note}, true
}

// unionManifest merges the two sides' project and link maps. Restore reads this to
// map a slug back to a real directory, so a machine missing from it is a machine
// whose history cannot be restored anywhere — the union is the only safe answer.
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
