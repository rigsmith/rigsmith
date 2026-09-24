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
	baselines, err := w.recordBaselines(ctx, pkgs)
	if err != nil {
		return nil, err
	}
	// Without a record, a package's last release is its highest release tag,
	// named as the tag step names it (RenderTag: `name@version`, a Go module's
	// `dir/vX.Y.Z`, a single app's `vX.Y.Z`, or the tagTemplate). A template
	// without ${name} is shared, so every package counts from the latest one.
	// Tags that can't be listed are an error, not "no tags": that would
	// count every package's whole history.
	tags, err := gitutil.MergedTags(ctx, w.Root)
	if err != nil {
		return nil, fmt.Errorf("listing release tags: %w", err)
	}
	solo := len(pkgs) == 1
	const at = "\x00" // stands in for the version, to split the tag around it
	for _, p := range pkgs {
		ref := baselines[p.Name]
		if ref == "" {
			rendered := gitutil.RenderTag(w.Config.TagTemplate, w.ecoOf[p.Name], p.Dir, p.Name, at, solo)
			if tag, found := gitutil.LatestTag(tags, strings.Split(rendered, at)); found {
				ref = tag
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
// (versioning.record), the commit that recorded its current release: a commit
// whose record holds the package's current version while none of its own
// parents' records does. That commit is the package's last release, so its
// commits since are what the next release is made of; the record wins over a
// tag, which can be deleted or never pushed. A package with no record entry,
// or no record kept, falls back to its tag, and so does every package in a
// shallow clone, whose cut-off history would make its oldest commit look like
// the one that recorded everything. An entry that isn't the package's current
// version is stale (a release went out while the record was off) and is
// passed over too.
func (w *Workspace) recordBaselines(ctx context.Context, pkgs []plugin.Package) (map[string]string, error) {
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
	if gitutil.IsShallow(ctx, w.Root) {
		return nil, nil
	}
	parents, err := gitutil.Parents(ctx, w.Root, history)
	if err != nil {
		return nil, err
	}
	// Every record the search can look at, read in one pass: HEAD's, each
	// commit's that changed the file, and each of their parents'.
	revs := append([]string{"HEAD"}, history...)
	for _, sha := range history {
		revs = append(revs, parents[sha]...)
	}
	files, err := gitutil.FileAtRevs(ctx, w.Root, revs, rel)
	if err != nil {
		return nil, err
	}
	records := map[string]map[string]string{}
	released := func(rev string) (map[string]string, error) {
		if r, ok := records[rev]; ok {
			return r, nil
		}
		var r map[string]string
		if data, ok := files[rev]; ok { // no file there is an empty record
			s, err := versionstate.Parse(data)
			if err != nil {
				return nil, fmt.Errorf("reading %s at %.12s: %w", rel, rev, err)
			}
			r = s.Released
		}
		records[rev] = r
		return r, nil
	}
	current, err := released("HEAD")
	if err != nil {
		return nil, err
	}
	stale := map[string]bool{}
	for _, p := range pkgs {
		if v, ok := current[p.Name]; ok && v != p.Version {
			stale[p.Name] = true
		}
	}
	fresh := make(map[string]string, len(current))
	for name, v := range current {
		if !stale[name] {
			fresh[name] = v
		}
	}
	current = fresh
	out := make(map[string]string, len(current))
	for _, sha := range history { // newest first
		after, err := released(sha)
		if err != nil {
			return nil, err
		}
		for name, v := range current {
			if _, found := out[name]; found || after[name] != v {
				continue
			}
			// A merge that took the entry from one side didn't record it:
			// that side's commit did, further down the history.
			set := true
			for _, p := range parents[sha] {
				before, err := released(p)
				if err != nil {
					return nil, err
				}
				if before[name] == v {
					set = false
					break
				}
			}
			if set {
				out[name] = sha
			}
		}
		if len(out) == len(current) {
			break
		}
	}
	return out, nil
}
