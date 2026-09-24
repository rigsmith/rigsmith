package commands

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/rigsmith/rigsmith/core/changeset"
	"github.com/rigsmith/rigsmith/core/commitsource"
	"github.com/rigsmith/rigsmith/core/config"
	"github.com/rigsmith/rigsmith/core/gitutil"
	"github.com/rigsmith/rigsmith/core/plugin"
	"github.com/rigsmith/rigsmith/core/since"
	"github.com/rigsmith/rigsmith/core/versionstate"
)

// LoadChangesets resolves the changesets a version/status run plans from,
// honoring the configured versioning source: on-disk changeset files
// ("changesets", the default), conventional commits ("commits"), or both. It is
// the single seam where commit-based versioning diverges — everything
// downstream (planner, cascade, grouping, prerelease, changelog) is shared,
// because commit mode produces ordinary in-memory changesets.
//
// fromCommits reports whether any changeset in the result was synthesized from a
// commit (so the caller can skip changeset-file bookkeeping like deletion and
// file-based changelog enrichment that only applies to on-disk changesets).
func (w *Workspace) LoadChangesets(ctx context.Context, pkgs []plugin.Package) (sets []*changeset.Changeset, fromCommits bool, err error) {
	return w.loadChangesets(ctx, pkgs, false)
}

// NarrowSince limits the rest of the run to what a branch adds since ref,
// whatever the versioning source: the changeset files it adds or edits, a
// prerelease's graduation only if the branch changed the prerelease state
// (Graduates), and the
// releases derived from the commits it adds, so a commit already on the base
// branch isn't the branch's. Commits are narrowed before a first release is
// collapsed, so its headline stands for the branch's commits only. It returns
// the changed files, read once, so a caller gating on them sees the same
// snapshot the plan is built from.
func (w *Workspace) NarrowSince(ctx context.Context, ref string) ([]string, error) {
	files, err := gitutil.ChangedFilesSince(ctx, w.Root, ref)
	if err != nil {
		return nil, fmt.Errorf("could not determine changes since %q: %w", ref, err)
	}
	commits, err := gitutil.CommitsSince(ctx, w.Root, ref)
	if err != nil {
		return nil, fmt.Errorf("could not determine commits since %q: %w", ref, err)
	}
	scope := &sinceScope{ids: map[string]bool{}, commits: commits}
	for _, id := range since.ChangedChangesetIDs(files, w.ChangesetDir) {
		scope.ids[id] = true
	}
	preFile := filepath.Join(w.ChangesetDir, "pre.json")
	for _, f := range files {
		if f == preFile {
			scope.prereleaseState = true
		}
	}
	w.since = scope
	return files, nil
}

// sinceScope is what a branch adds since a ref: the ids of the changeset
// files it changed, its commits' SHAs, and whether it changed the prerelease
// state (.changeset/pre.json).
type sinceScope struct {
	ids             map[string]bool
	commits         map[string]bool
	prereleaseState bool
}

// Graduates reports whether the run may graduate a prerelease (the run after
// `pre exit`): always, unless narrowed to a branch that didn't change the
// prerelease state, since then the graduation is the base branch's, not the
// branch's.
func (w *Workspace) Graduates() bool {
	return w.since == nil || w.since.prereleaseState
}

// keeps reports whether a changeset is the branch's: a file it changed, or a
// release from one of its commits. A nil scope keeps everything.
func (s *sinceScope) keeps(cs *changeset.Changeset) bool {
	if s == nil {
		return true
	}
	if cs.Commit != "" {
		return s.commits[cs.Commit]
	}
	return s.ids[cs.ID]
}

// LoadPendingChangesets is LoadChangesets for a caller that only reports what
// would release (a package listing), not a changesets command: a missing
// .changeset/ directory reads as no changesets on disk rather than an error,
// and commit-derived changesets are still read. `status` and `version` keep
// the strict LoadChangesets, as `changeset status` requires the folder.
func (w *Workspace) LoadPendingChangesets(ctx context.Context, pkgs []plugin.Package) (sets []*changeset.Changeset, fromCommits bool, err error) {
	return w.loadChangesets(ctx, pkgs, true)
}

func (w *Workspace) loadChangesets(ctx context.Context, pkgs []plugin.Package, missingDirOK bool) (sets []*changeset.Changeset, fromCommits bool, err error) {
	if w.Config.UsesChangesets() {
		onDisk, err := changeset.Dir(w.ChangesetDir, "")
		switch {
		case err == nil:
			for _, cs := range onDisk {
				if w.since.keeps(cs) {
					sets = append(sets, cs)
				}
			}
		case missingDirOK && errors.Is(err, fs.ErrNotExist):
			// No .changeset/ at all: nothing on disk, and on to commits.
		default:
			return nil, false, fmt.Errorf("reading changesets: %w", err)
		}
	}
	if w.Config.UsesCommits() {
		derived, err := w.commitChangesets(ctx, pkgs)
		if err != nil {
			return nil, false, err
		}
		sets = append(sets, derived...)
		fromCommits = len(derived) > 0 || w.Config.CommitSource() == config.SourceCommits
	}
	return sets, fromCommits, nil
}

// commitChangesets synthesizes changesets from the commits since each package's
// last release tag. The since-ref is per-package (each module carries its own
// tag, e.g. `core/v1.2.0` vs `v1.2.0`), so packages released at different times
// each see only their own new commits. Packages sharing a since-ref share one
// `git log`.
// Narrowed (NarrowSince), only the branch's commits count.
func (w *Workspace) commitChangesets(ctx context.Context, pkgs []plugin.Package) ([]*changeset.Changeset, error) {
	// Bucket packages by their since-ref so each distinct ref is logged once.
	refOf := map[string]string{}
	pkgsByRef := map[string][]string{}
	baselines, err := w.recordBaselines(ctx)
	if err != nil {
		return nil, err
	}
	for _, p := range pkgs {
		ref := baselines[p.Name]
		if ref == "" {
			if v, ok := gitutil.LatestModuleVersion(ctx, w.Root, p.Dir); ok {
				ref = gitutil.ModuleTag(p.Dir, v)
			}
		}
		refOf[p.Name] = ref
		pkgsByRef[ref] = append(pkgsByRef[ref], p.Name)
	}

	collapseInitial := w.Config.Versioning.InitialRelease.Collapse

	var out []*changeset.Changeset
	for ref, names := range pkgsByRef {
		commits, err := gitutil.LogSince(ctx, w.Root, ref)
		if err != nil {
			return nil, fmt.Errorf("reading commits since %q: %w", ref, err)
		}
		// Attribute against the full package set (so deepest-package wins), then
		// keep only the releases for packages whose since-ref is this ref — a
		// commit must not bump a package across a different release window.
		want := make(map[string]bool, len(names))
		for _, n := range names {
			want[n] = true
		}
		var refSets []*changeset.Changeset
		for _, cs := range commitsource.Synthesize(commits, pkgs, w.Root, w.Config) {
			if !w.since.keeps(cs) {
				continue
			}
			var kept []changeset.Release
			for _, r := range cs.Releases {
				if want[r.Name] {
					kept = append(kept, r)
				}
			}
			if len(kept) == 0 {
				continue
			}
			clone := *cs
			clone.Releases = kept
			refSets = append(refSets, &clone)
		}
		// First release (empty since-ref → no prior tag) with collapse enabled:
		// condense the whole history into one "Initial release" line per package
		// so the changelog isn't a dump of every commit. Tagged refs are
		// unaffected and keep their per-commit changesets.
		if collapseInitial && ref == "" {
			refSets = collapseInitialRelease(refSets, w.Config)
		}
		out = append(out, refSets...)
	}
	return out, nil
}

// collapseInitialRelease replaces a first release's per-commit changesets with a
// single synthetic changeset per package — one configured summary line at a
// fixed bump — so a package's debut changelog is a headline, not its full
// history. Packages are emitted in first-seen order for a stable changelog.
func collapseInitialRelease(sets []*changeset.Changeset, cfg *config.Config) []*changeset.Changeset {
	bump := cfg.InitialReleaseBump()
	summary := cfg.InitialReleaseSummary()

	var order []string
	seen := map[string]bool{}
	for _, cs := range sets {
		for _, r := range cs.Releases {
			if !seen[r.Name] {
				seen[r.Name] = true
				order = append(order, r.Name)
			}
		}
	}
	out := make([]*changeset.Changeset, 0, len(order))
	for _, name := range order {
		out = append(out, &changeset.Changeset{
			Releases: []changeset.Release{{Name: name, Bump: bump}},
			Summary:  summary,
			// Package names carry path separators (e.g. "example.com/a"); flatten
			// them so the synthetic ID stays a single-segment file-name stem and
			// can't turn an ID+".md" join into a nested path.
			ID: "initial-" + strings.ReplaceAll(name, "/", "-"),
		})
	}
	return out
}

// recordBaselines finds, for each package in the committed release record
// (versioning.record), the commit that recorded its current release: the
// newest commit that changed its `released` entry in .changeset/versions.json.
// That commit is the package's last release, so its commits since are what
// the next release is made of; the record wins over a tag, which can be
// deleted, never pushed, or (outside Go's module tags) not found at all. A
// package with no record entry, or no record kept, falls back to its tag.
func (w *Workspace) recordBaselines(ctx context.Context) (map[string]string, error) {
	if !w.Config.Versioning.Record {
		return nil, nil
	}
	rel, err := filepath.Rel(w.Root, filepath.Join(w.ChangesetDir, versionstate.FileName))
	if err != nil {
		return nil, nil
	}
	history, err := gitutil.FileHistory(ctx, w.Root, rel)
	if err != nil || len(history) == 0 {
		return nil, nil // not committed yet (or no git): tags, as before
	}
	released := func(rev string) (map[string]string, error) {
		data, ok := gitutil.ShowFile(ctx, w.Root, rev, rel)
		if !ok {
			return nil, nil
		}
		s, err := versionstate.Parse(data)
		if err != nil {
			return nil, fmt.Errorf("reading %s at %.12s: %w", rel, rev, err)
		}
		return s.Released, nil
	}
	current, err := released(history[0])
	if err != nil {
		return nil, err
	}
	// Newest first, a package's baseline is the first commit whose previous
	// record doesn't hold its current version: every newer commit left the
	// entry as it is, so this one set it.
	out := make(map[string]string, len(current))
	for i, sha := range history {
		// The record as it stood before this commit: at the next-older
		// commit that changed it (nothing in between did).
		var before map[string]string
		if i+1 < len(history) {
			if before, err = released(history[i+1]); err != nil {
				return nil, err
			}
		}
		for name, v := range current {
			if _, found := out[name]; !found && before[name] != v {
				out[name] = sha
			}
		}
		if len(out) == len(current) {
			break
		}
	}
	return out, nil
}
