// Package mergepolicy settles the conflicts a shared sync repo produces, so two
// machines that both pushed do not need a person with a mergetool.
//
// Most of what lands in the repo is designed not to conflict at all — the
// journal is one file per machine, and each machine has its own sessions. What
// is left is a small, named set, and each one has an answer that is obviously
// right rather than a guess:
//
//   - the manifest is a union of working directories;
//   - the device registry takes the newer record per device;
//   - a config file takes whichever side was committed later;
//   - a rollout takes the longer side ONLY when the shorter is its prefix.
//
// That last rule is the one worth arguing about, and the narrowness is the
// point. A rollout is append-only, so a prefix relationship means one machine
// simply has more of the same session and taking the longer loses nothing.
// Anything else means the two sides diverged — a compaction, a fork, a repaired
// file — and line-unioning divergent tool-call histories produces a transcript
// that never happened. Those are left for a person, which is the honest outcome.
package mergepolicy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/rigsmith/rigsmith/internal/codexrig/rolloutstore"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/internal/codexrig/devices"
	"github.com/rigsmith/rigsmith/internal/codexrig/manifest"
	"github.com/rigsmith/rigsmith/internal/codexrig/rollout"
)

// Policy names how a conflict was settled, for the report.
type Policy string

const (
	PolicyUnion  Policy = "union"   // both sides' entries kept
	PolicyNewest Policy = "newest"  // the later commit won
	PolicyAppend Policy = "append"  // one side was a prefix of the other
	PolicyKeep   Policy = "kept"    // identical on both sides
	PolicyDrop   Policy = "dropped" // a chunk part no kept index references
)

// Resolution is one settled file.
type Resolution struct {
	Path   string
	Policy Policy
	Note   string

	// index is the chunk index this resolution kept, when the file was one.
	// Its parts are settled against it in Resolve's second pass.
	index *rolloutstore.Index
}

// Report is the outcome of a reconcile.
type Report struct {
	Resolved   []Resolution
	Unresolved []string
}

// Resolve settles what it can in an in-progress merge and stages the results.
// It does NOT commit: the caller has to run the tripwire over the merged tree
// before anything is published, and committing here would put that check after
// the point of no return.
func Resolve(ctx context.Context, repo *gitrepo.Repo) (Report, error) {
	var rep Report
	paths, err := repo.UnmergedPaths(ctx)
	if err != nil {
		return rep, err
	}
	// Rollouts first, their parts second. A chunked rollout's parts conflict
	// alongside its index — git's rename detection reads "old last part gone,
	// new last part added" as two competing renames — and which parts survive
	// is decided by which INDEX survives, so the index has to be settled
	// before its parts can be.
	kept := map[string]*rolloutstore.Index{}
	unresolved := map[string]bool{}
	var parts []string
	for _, p := range paths {
		if rolloutstore.IsPartPath(p) {
			parts = append(parts, p)
			continue
		}
		res, ok, err := resolveOne(ctx, repo, p)
		if err != nil {
			return rep, err
		}
		if !ok {
			rep.Unresolved = append(rep.Unresolved, p)
			unresolved[p] = true
			continue
		}
		rep.Resolved = append(rep.Resolved, res)
		if res.index != nil {
			kept[p] = res.index
		}
	}
	for _, p := range parts {
		res, ok, err := resolvePart(ctx, repo, p, kept, unresolved)
		if err != nil {
			return rep, err
		}
		if !ok {
			rep.Unresolved = append(rep.Unresolved, p)
			continue
		}
		rep.Resolved = append(rep.Resolved, res)
	}
	return rep, nil
}

// resolvePart settles one conflicted chunk part against the index that owns it.
// Parts are content-addressed and never edited, so there is only one question:
// does the index this merge is keeping reference it. Yes: keep the bytes, hash-
// checked, from wherever the merge left them intact. No: it is a leftover of
// the side that lost, and leaving it would publish bytes no index vouches for.
func resolvePart(ctx context.Context, repo *gitrepo.Repo, p string, kept map[string]*rolloutstore.Index, unresolved map[string]bool) (Resolution, bool, error) {
	owner, hash, ok := rolloutstore.SplitPartPath(p)
	if !ok {
		return Resolution{}, false, nil
	}
	if unresolved[owner] {
		return Resolution{}, false, nil // the index is a human's problem, so its parts are too
	}
	idx := kept[owner]
	if idx == nil {
		// The index did not conflict, or was settled before this merge: read
		// what the working tree holds.
		raw, err := os.ReadFile(filepath.Join(repo.Dir, filepath.FromSlash(owner)))
		if err != nil || !rolloutstore.IsIndex(raw) {
			return Resolution{}, false, nil //nolint:nilerr // no index to judge by
		}
		if idx, err = rolloutstore.Decode(raw); err != nil {
			return Resolution{}, false, nil //nolint:nilerr
		}
	}
	if !idx.References(hash) {
		if err := repo.RemovePath(ctx, p); err != nil {
			return Resolution{}, false, err
		}
		return Resolution{Path: p, Policy: PolicyDrop, Note: "a part of the side that had less"}, true, nil
	}
	body, found := partBytes(ctx, repo, p, hash)
	if !found {
		return Resolution{}, false, nil
	}
	if err := repo.ResolveWith(ctx, p, body); err != nil {
		return Resolution{}, false, err
	}
	return Resolution{Path: p, Policy: PolicyKeep, Note: "a part the kept rollout references"}, true, nil
}

// partBytes finds a part's content wherever the merge left it intact, verifying
// the hash: a part is named by its content, so a mismatch is not this part.
//
// The working tree first, then the two COMMITS being merged — not the index
// stages. For a rename/rename conflict git writes the merged, marker-laden
// content to the destination paths and records that same content in the
// stages, so neither holds either side's real bytes. HEAD and MERGE_HEAD do.
func partBytes(ctx context.Context, repo *gitrepo.Repo, p, hash string) ([]byte, bool) {
	if b, err := os.ReadFile(filepath.Join(repo.Dir, filepath.FromSlash(p))); err == nil && rolloutstore.HashOf(b) == hash {
		return b, true
	}
	for _, ref := range []string{"HEAD", "MERGE_HEAD"} {
		if b, err := repo.ShowFile(ctx, ref, p); err == nil && rolloutstore.HashOf(b) == hash {
			return b, true
		}
	}
	return nil, false
}

func resolveOne(ctx context.Context, repo *gitrepo.Repo, p string) (Resolution, bool, error) {
	_, ours, theirs, err := repo.ConflictStages(ctx, p)
	if err != nil {
		return Resolution{}, false, nil //nolint:nilerr // unreadable stages mean a human, not a failed sync
	}
	// A delete on one side and a modify on the other is a decision about intent,
	// not about content. Nothing here can make it.
	if ours == nil || theirs == nil {
		return Resolution{}, false, nil
	}
	if bytes.Equal(ours, theirs) {
		if err := repo.ResolveWith(ctx, p, ours); err != nil {
			return Resolution{}, false, err
		}
		return Resolution{Path: p, Policy: PolicyKeep, Note: "identical on both sides"}, true, nil
	}

	base := path.Base(p)
	switch {
	case base == manifest.FileName:
		merged, err := unionManifest(ours, theirs)
		if err != nil {
			return Resolution{}, false, nil //nolint:nilerr // an unparseable manifest is a human's problem
		}
		if err := repo.ResolveWith(ctx, p, merged); err != nil {
			return Resolution{}, false, err
		}
		return Resolution{Path: p, Policy: PolicyUnion, Note: "working directories from both machines"}, true, nil

	case base == devices.FileName:
		merged, err := unionDevices(ours, theirs)
		if err != nil {
			return Resolution{}, false, nil //nolint:nilerr
		}
		if err := repo.ResolveWith(ctx, p, merged); err != nil {
			return Resolution{}, false, err
		}
		return Resolution{Path: p, Policy: PolicyUnion, Note: "the newer record per machine"}, true, nil

	case isRollout(p):
		// Append-only, so a prefix relationship is the only safe merge — of
		// what the rollout SAYS. Past the chunking threshold each side is an
		// index over hashed parts, and two indexes are never a byte-prefix of
		// each other even when one conversation is exactly the other plus a
		// turn: the last part was partial and got replaced. So compare the
		// logical bytes, and keep the index of whichever side is longer.
		oursLogical, oerr := logical(ctx, repo, p, ours)
		theirsLogical, terr := logical(ctx, repo, p, theirs)
		if oerr != nil || terr != nil {
			return Resolution{}, false, nil //nolint:nilerr // an index whose parts cannot be read is a human's problem
		}
		if longer, ok := appendOnly(oursLogical, theirsLogical); ok {
			keep := ours
			if bytes.Equal(longer, theirsLogical) {
				keep = theirs
			}
			if err := repo.ResolveWith(ctx, p, keep); err != nil {
				return Resolution{}, false, err
			}
			res := Resolution{Path: p, Policy: PolicyAppend, Note: "one side had more of the same session"}
			if rolloutstore.IsIndex(keep) {
				res.index, _ = rolloutstore.Decode(keep)
			}
			return res, true, nil
		}
		// Diverged. Line-unioning two tool-call histories produces a transcript
		// that never happened, so this goes to a person.
		return Resolution{}, false, nil

	case strings.HasSuffix(p, ".toml"), strings.HasSuffix(p, ".json"):
		newer, note, ok, err := newest(ctx, repo, p, ours, theirs)
		if err != nil || !ok {
			return Resolution{}, false, err
		}
		if err := repo.ResolveWith(ctx, p, newer); err != nil {
			return Resolution{}, false, err
		}
		return Resolution{Path: p, Policy: PolicyNewest, Note: note}, true, nil
	}
	return Resolution{}, false, nil
}

// isRollout strips the staging root's first segment (the root id) before asking,
// because paths here are repo-relative and rollout paths are root-relative.
func isRollout(p string) bool {
	if _, rest, ok := strings.Cut(p, "/"); ok {
		return rollout.IsRolloutRel(rest)
	}
	return false
}

// logical is the rollout's own bytes: the file itself when plain, the parts
// assembled when it is a chunk index. Parts come through partBytes, which
// knows where a merge leaves them intact.
func logical(ctx context.Context, repo *gitrepo.Repo, p string, side []byte) ([]byte, error) {
	if !rolloutstore.IsIndex(side) {
		return side, nil
	}
	idx, err := rolloutstore.Decode(side)
	if err != nil {
		return nil, err
	}
	return rolloutstore.Assemble(idx, func(hash string) ([]byte, error) {
		b, ok := partBytes(ctx, repo, rolloutstore.PartPath(p, hash), hash)
		if !ok {
			return nil, fmt.Errorf("no copy matching its hash in the working tree or either merged commit")
		}
		return b, nil
	})
}

// appendOnly returns the longer side when the shorter is its exact prefix.
func appendOnly(a, b []byte) ([]byte, bool) {
	if len(a) > len(b) && bytes.HasPrefix(a, b) {
		return a, true
	}
	if len(b) > len(a) && bytes.HasPrefix(b, a) {
		return b, true
	}
	return nil, false
}

func unionManifest(ours, theirs []byte) ([]byte, error) {
	var a, b manifest.Manifest
	if err := json.Unmarshal(ours, &a); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(theirs, &b); err != nil {
		return nil, err
	}
	// Ours wins per key, theirs fills the gaps — the same rule sync itself uses,
	// so a merge and a sync cannot disagree about who owns an entry.
	a.MergeFrom(&b)
	out, err := json.MarshalIndent(&a, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

func unionDevices(ours, theirs []byte) ([]byte, error) {
	var a, b devices.Registry
	if err := json.Unmarshal(ours, &a); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(theirs, &b); err != nil {
		return nil, err
	}
	if a.Devices == nil {
		a.Devices = map[string]devices.Device{}
	}
	for name, d := range b.Devices {
		cur, have := a.Devices[name]
		if !have || d.LastSync.After(cur.LastSync) {
			// A record with no account must not erase one that has it: a run
			// that could not read the login is not evidence there is none.
			if have && d.Account == nil {
				d.Account = cur.Account
			}
			a.Devices[name] = d
		}
	}
	out, err := json.MarshalIndent(&a, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// newest picks the side whose commit is later, with ties going to THEIRS so
// every machine settles the same way and the fleet converges instead of
// ping-ponging.
func newest(ctx context.Context, repo *gitrepo.Repo, p string, ours, theirs []byte) ([]byte, string, bool, error) {
	ourAt, okOur := repo.SideCommitTime(ctx, "HEAD", p)
	theirAt, okTheir := repo.SideCommitTime(ctx, "MERGE_HEAD", p)
	if !okOur || !okTheir {
		return nil, "", false, nil
	}
	if ourAt.After(theirAt) {
		return ours, "this machine's copy is newer (" + stamp(ourAt) + ")", true, nil
	}
	return theirs, "the remote's copy is newer or the same age (" + stamp(theirAt) + ")", true, nil
}

func stamp(t time.Time) string { return t.UTC().Format("2006-01-02 15:04") }

// Describe renders a report for a terminal.
func (r Report) Describe() string {
	var b strings.Builder
	for _, res := range r.Resolved {
		fmt.Fprintf(&b, "  %s  %s — %s\n", res.Policy, res.Path, res.Note)
	}
	for _, p := range r.Unresolved {
		fmt.Fprintf(&b, "  needs a person  %s\n", p)
	}
	return b.String()
}
