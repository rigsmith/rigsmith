package cli

import (
	"context"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/rigsmith/rigsmith/core/ecosystem"
	"github.com/rigsmith/rigsmith/core/match"
	"github.com/rigsmith/rigsmith/core/plugin"
	"github.com/rigsmith/rigsmith/core/walkutil"
	"github.com/rigsmith/rigsmith/internal/rig/config"
	"github.com/rigsmith/rigsmith/internal/rig/detect"
)

// target is a discovered workspace package: its name, owning ecosystem, absolute
// directory, intra-repo dependency names (for topological ordering), and — for
// ecosystems that distinguish them — whether it is a runnable executable and/or
// a test project.
type target struct {
	Name     string
	Eco      string
	Dir      string // absolute
	Version  string // current version when the ecosystem tracks one ("" otherwise)
	Deps     []string
	Runnable bool // produces an executable (consulted by isRunnable for .NET)
	IsTest   bool // a test project
}

// shortName is the last '/'-segment of a (possibly slashy) package name.
func (t target) shortName() string {
	return shortName(t.Name)
}

// discoverWorkspace returns every package across every applicable ecosystem,
// tagged with its ecosystem id and absolute dir. Packages matching the
// `exclude` globs (by full or short name) are dropped, keeping discovery and the
// pickers consistent with `info`. Best-effort: discovery errors for one
// ecosystem are skipped.
//
// .NET is sourced from the convention-first project model (detect.DiscoverDotNet)
// rather than the ecosystem adapter's Discover: the adapter is release-oriented
// and only reports version-bearing projects (a NuGet concern), which hides app
// and test projects from the dev verbs and pickers. The dev model is
// solution-aware, version-independent, and carries runnable/test classification.
//
// Overlay ecosystems (velopack, electron, tauri — those declaring Overlays) are
// skipped: they don't own dev targets, they re-emit the base-language project
// beside their packaging file (a .csproj / package.json / Cargo.toml) for the
// release path. Surfacing them here double-counts that project — and since
// topoSort keys by name, the overlay copy (which maps no dev verb, so `rig run`
// can't run it) would shadow the base, dropping the real target from the run/
// build/test lists. Dev verbs act on the base; let it own the unit.
func discoverWorkspace(ctx context.Context, root string, exclude []string) []target {
	var out []target
	for _, eco := range ecosystem.Default().All() {
		if len(eco.Info().Overlays) > 0 {
			continue
		}
		ok, err := eco.Detect(ctx, root)
		if err != nil || !ok {
			continue
		}
		id := eco.Info().ID
		if id == detect.DotNet {
			out = append(out, dotnetTargets(root, exclude)...)
			continue
		}
		resp, err := eco.Discover(ctx, plugin.DiscoverRequest{RepoRoot: root, SourcePath: "."})
		if err != nil {
			continue
		}
		for _, p := range resp.Packages {
			deps := make([]string, 0, len(p.Dependencies))
			for _, d := range p.Dependencies {
				deps = append(deps, d.Name)
			}
			t := target{Name: p.Name, Eco: id, Dir: filepath.Join(root, p.Dir), Version: p.Version, Deps: deps, Runnable: true}
			if projectExcluded(t.Name, t.shortName(), relSlash(root, t.Dir), exclude) {
				continue
			}
			out = append(out, t)
		}
	}
	// A nested git worktree holds a complete copy of the workspace; discovery
	// skips it so its projects don't shadow the real ones (see nestedwt.go).
	return dropNestedWorktrees(root, out)
}

// runEntry is one run target plus whether the current .rig.json `exclude` globs
// hide it. The pickers use the excluded flag to optionally show excluded rows
// (dimmed) so a user can re-include them; runTargets is the plain filtered view.
type runEntry struct {
	t        target
	excluded bool
}

// runTargetEntries is the workspace's full set of run targets for `rig run`,
// each marked excluded-or-not — UNFILTERED, so the picker can reveal excluded
// rows. Non-Go targets pass through from discovery; each Go module is expanded
// into one target per `package main` directory it holds, so a multi-binary Go
// repo offers cmd/rig, cmd/clauderig, … instead of the module root (which is
// not itself runnable when the mains live under cmd/). A Go module with no main
// contributes nothing. Exclusion is matched by binary name, short name, and
// repo-relative path, so a glob can hide one binary or a whole directory.
func runTargetEntries(ctx context.Context, root string) []runEntry {
	exclude := excludeFor(root)
	var out []runEntry
	for _, t := range discoverWorkspace(ctx, root, nil) {
		if t.Eco != detect.Go {
			rel := relSlash(root, t.Dir)
			out = append(out, runEntry{t: t, excluded: projectExcluded(t.Name, t.shortName(), rel, exclude)})
			continue
		}
		for _, rel := range goMainDirs(t.Dir, root) {
			// The walk descends the whole module, so a nested worktree inside it
			// would contribute a second copy of every binary — skip those.
			if inNestedWorktree(root, rel) {
				continue
			}
			name := path.Base(rel)
			if rel == "." {
				name = t.shortName()
			}
			mt := target{Name: name, Eco: detect.Go, Dir: filepath.Join(root, filepath.FromSlash(rel)), Runnable: true}
			out = append(out, runEntry{t: mt, excluded: projectExcluded(name, "", rel, exclude)})
		}
	}
	return out
}

// runTargets is the filtered run-target list (excluded entries dropped) for
// `rig run <name>` and the default run path.
func runTargets(ctx context.Context, root string) []target {
	var out []target
	for _, e := range runTargetEntries(ctx, root) {
		if !e.excluded {
			out = append(out, e.t)
		}
	}
	return out
}

// relSlash returns dir relative to root as a '/'-separated path ("." at the
// root). Falls back to the absolute dir if it can't be made relative.
func relSlash(root, dir string) string {
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return dir
	}
	return filepath.ToSlash(rel)
}

// goMainDirs returns the repo-relative slash directories of every `package main`
// under moduleDir, via the shared gitignore-aware walk (build output, vendor, and
// dependency trees are skipped). It is the per-binary expansion behind runTargets.
func goMainDirs(moduleDir, root string) []string {
	seen := map[string]bool{}
	var dirs []string
	_ = walkutil.Walk(moduleDir, func(p string, d fs.DirEntry) error {
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		if !fileDeclaresMainPackage(p) {
			return nil
		}
		rel, err := filepath.Rel(root, filepath.Dir(p))
		if err != nil {
			return nil
		}
		if rel = filepath.ToSlash(rel); !seen[rel] {
			seen[rel] = true
			dirs = append(dirs, rel)
		}
		return nil
	})
	sort.Strings(dirs)
	return dirs
}

// discoverDotnet is the single entry point for the .NET project scan: it is
// detect.DiscoverDotNet plus rig's nested-worktree rule. Without a root solution
// the scanner walks the whole tree, so a nested worktree hands back a second
// copy of every project — which is how a duplicate reaches `publish`, `default`,
// `outdated`, or `rebuild` (where it would clean bin/obj in the wrong checkout).
// Every caller in the CLI goes through here so the rule holds everywhere, not
// just in discoverWorkspace.
func discoverDotnet(root, solution string, exclude []string) []detect.ProjectInfo {
	projects := detect.DiscoverDotNet(root, solution, exclude)
	if includeWorktrees {
		return projects
	}
	out := projects[:0:0]
	for _, p := range projects {
		if _, nested := nestedWorktreeFor(root, filepath.Dir(p.FullPath)); nested {
			continue
		}
		out = append(out, p)
	}
	return out
}

// dotnetTargets discovers the repo's .NET projects via the convention-first dev
// model (solution-aware, version-independent), carrying each project's runnable
// and test classification and its intra-repo project-reference dependencies.
// discoverDotnet applies the exclude globs (via detect) and the nested-worktree
// rule.
func dotnetTargets(root string, exclude []string) []target {
	cfg, _ := config.LoadMerged(root)
	var out []target
	for _, p := range discoverDotnet(root, cfg.Solution, exclude) {
		out = append(out, target{
			Name:     p.Name,
			Eco:      detect.DotNet,
			Dir:      filepath.Dir(p.FullPath),
			Deps:     p.Deps,
			Runnable: p.IsRunnable(),
			IsTest:   p.IsTest,
		})
	}
	return out
}

// topoSort orders targets so a package's intra-repo dependencies come before it
// (Kahn's algorithm). It is cycle-tolerant: any targets left in a cycle are
// appended in stable name order. Ties are broken by name for deterministic runs.
//
// Duplicate-named targets (the same project checked out in several paths — e.g. a
// nested git worktree) are all kept, not collapsed to one: callers surface them
// with their paths so discovery, the pickers, and `--all` agree on what exists.
// The dependency graph is a node per name (all copies of a name share it and emit
// together), which keeps ordering correct while preserving every copy.
func topoSort(targets []target) []target {
	byName := make(map[string][]target, len(targets))
	var nameOrder []string // first-seen order, for stable output
	for _, t := range targets {
		if _, seen := byName[t.Name]; !seen {
			nameOrder = append(nameOrder, t.Name)
		}
		byName[t.Name] = append(byName[t.Name], t)
	}

	// deps/dependents at the name level (deduped, so duplicate-named copies don't
	// double-count an edge). indegree = number of distinct in-repo deps.
	deps := map[string]map[string]bool{}
	dependents := map[string][]string{} // dep -> names that depend on it
	for _, name := range nameOrder {
		deps[name] = map[string]bool{}
	}
	for _, t := range targets {
		for _, d := range t.Deps {
			if _, ok := byName[d]; ok && d != t.Name && !deps[t.Name][d] {
				deps[t.Name][d] = true
				dependents[d] = append(dependents[d], t.Name)
			}
		}
	}
	indeg := map[string]int{}
	for _, name := range nameOrder {
		indeg[name] = len(deps[name])
	}

	var ready []string
	for _, name := range nameOrder {
		if indeg[name] == 0 {
			ready = append(ready, name)
		}
	}
	sort.Strings(ready)

	var order []target
	emitted := map[string]bool{}
	for len(ready) > 0 {
		name := ready[0]
		ready = ready[1:]
		if emitted[name] {
			continue
		}
		emitted[name] = true
		order = append(order, byName[name]...)

		var newly []string
		for _, dep := range dependents[name] {
			indeg[dep]--
			if indeg[dep] == 0 {
				newly = append(newly, dep)
			}
		}
		sort.Strings(newly)
		ready = append(ready, newly...)
	}

	// Append anything caught in a cycle, in stable name order.
	if len(order) < len(targets) {
		var restNames []string
		for _, name := range nameOrder {
			if !emitted[name] {
				restNames = append(restNames, name)
			}
		}
		sort.Strings(restNames)
		for _, name := range restNames {
			order = append(order, byName[name]...)
		}
	}
	return order
}

// duplicateNames returns the set of names shared by more than one target — the
// projects whose path must be shown to tell them apart. A duplicate is usually
// one project checked out in several places (e.g. a nested worktree).
func duplicateNames(targets []target) map[string]bool {
	count := map[string]int{}
	for _, t := range targets {
		count[t.Name]++
	}
	dup := map[string]bool{}
	for name, n := range count {
		if n > 1 {
			dup[name] = true
		}
	}
	return dup
}

// filterTargets keeps targets whose name or short name matches the glob.
func filterTargets(targets []target, glob string) []target {
	if glob == "" {
		return targets
	}
	var out []target
	for _, t := range targets {
		if globMatch(glob, t.Name) || globMatch(glob, t.shortName()) {
			out = append(out, t)
		}
	}
	return out
}

// minMatchTier is the loosest tier a project selector may resolve through:
// substring. Subsequence (match.Tier 40) is deliberately out of reach here —
// verbs like `rig test <class>` pass through args that name no project, and a
// subsequence match would hijack them (almost any short string is a
// subsequence of some project name). `rig cd`, whose whole job is navigation,
// does accept subsequences.
const minMatchTier = 60

// nameTier scores a query against a project name's three forms — the full name,
// the slash-short name (node scopes, Go module paths), and the dot-short name (a
// .NET project's trailing segment) — taking the best. Tiers are the shared
// exact > prefix > substring > subsequence ladder from core/match. Every
// project selector in rig scores through this one function, whether the query
// came from an argument or from `defaultProject`.
func nameTier(name, query string) int {
	return max(
		match.Tier(name, query),
		match.Tier(shortName(name), query),
		match.Tier(dotShortName(name), query),
	)
}

// targetTier scores a query against a discovered target's name.
func targetTier(t target, query string) int { return nameTier(t.Name, query) }

// topTierNames resolves a query over a list of project names and returns the
// winning tier's names. It answers "which rows would a bare `rig run` consider?"
// for callers that hold names rather than targets — the picker's ★ default
// marker and `rig info`'s duplicate labelling — so what they point at is what
// would actually launch. Empty when the query names nothing.
func topTierNames(names []string, query string) map[string]bool {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil
	}
	best := 0
	out := map[string]bool{}
	for _, n := range names {
		switch tier := nameTier(n, q); {
		case tier < minMatchTier:
		case tier > best:
			best, out = tier, map[string]bool{n: true}
		case tier == best:
			out[n] = true
		}
	}
	return out
}

// matchTargets returns the targets matching query in the BEST non-empty tier
// only: every exact match if any exist, else every prefix match, else every
// substring match (see minMatchTier). An empty query returns nil.
//
// Tiering is what makes "one exact hit is never ambiguous" true: "Tweed.App"
// resolves to the project of that name and stops, instead of also dragging in
// Tweed.App.Tests (prefix) and Tweed.AcpAgent.Sample (looser still).
//
// Unlike matchTarget it keeps every match in that tier — so a name shared by
// several paths (a duplicate) surfaces as multiple results the caller can offer
// in a picker or list, rather than being silently dropped as "ambiguous". The
// name semantics mirror defaultMatches so arg resolution and the configured
// defaultProject agree.
func matchTargets(targets []target, query string) []target {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil
	}
	best := 0
	var out []target
	for _, t := range targets {
		switch tier := targetTier(t, q); {
		case tier < minMatchTier:
		case tier > best:
			best, out = tier, []target{t}
		case tier == best:
			out = append(out, t)
		}
	}
	return out
}

// matchTarget resolves a query to a single unambiguous target (see
// matchTargets). ok is false when nothing matches or several do; callers that
// want to offer a picker on ambiguity use matchTargets directly.
func matchTarget(targets []target, query string) (target, bool) {
	if m := matchTargets(targets, query); len(m) == 1 {
		return m[0], true
	}
	return target{}, false
}

// matchDefaultProject resolves a configured defaultProject to a single target
// through the SAME rules an explicit argument resolves through — the tiered
// matcher, then proximity to cwd. A config value and a typed name must never
// disagree about which project they mean; that divergence is what let a bare
// `rig run` launch a copy an explicit `rig run <name>` refused.
//
// Tiering is what keeps a value like "Desktop" scoped to "Acme.Desktop" instead
// of going ambiguous against "Acme.Desktop.Tests": the dot-short exact match
// wins its tier outright and the substring match never enters. ok is false when
// the default names nothing — or names several copies, since an ambiguous
// default is not a selection.
func matchDefaultProject(targets []target, defaultProject string) (target, bool) {
	cwd, _ := os.Getwd()
	if m := nearestTargets(cwd, matchTargets(targets, defaultProject)); len(m) == 1 {
		return m[0], true
	}
	return target{}, false
}

// nearestTargets narrows equally-good matches by proximity to cwd (see
// nearestByDir).
func nearestTargets(cwd string, ts []target) []target {
	return nearestByDir(cwd, ts, func(t target) string { return t.Dir })
}

// nearestByDir narrows a set of equally-good matches by proximity to cwd — the
// tiebreak the rest of the ecosystem applies. The nearest ENCLOSING project
// wins outright (you are standing in it); failing that, the items sharing the
// longest directory prefix with cwd win (the nearest common ancestor). When
// nothing distinguishes them — the usual case at a repo root, where every copy
// is equally far away — items comes back unchanged and the caller reports the
// ambiguity instead of guessing.
func nearestByDir[T any](cwd string, items []T, dirOf func(T) string) []T {
	if len(items) < 2 || strings.TrimSpace(cwd) == "" {
		return items
	}
	// An enclosing project: cwd is inside its directory. The deepest such
	// directory is the innermost project around you.
	var enclosing []T
	deepest := -1
	for _, it := range items {
		if !dirContains(dirOf(it), cwd) {
			continue
		}
		switch n := len(splitDirSegments(dirOf(it))); {
		case n > deepest:
			deepest, enclosing = n, []T{it}
		case n == deepest:
			enclosing = append(enclosing, it)
		}
	}
	if len(enclosing) > 0 {
		return enclosing
	}
	// Otherwise: the nearest common ancestor with cwd.
	best := 0
	var out []T
	for _, it := range items {
		switch n := sharedDirSegments(dirOf(it), cwd); {
		case n > best:
			best, out = n, []T{it}
		case n == best:
			out = append(out, it)
		}
	}
	if len(out) == 0 || len(out) == len(items) {
		return items // nothing to choose between
	}
	return out
}

// dirContains reports whether dir is path itself or one of its ancestors.
func dirContains(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel))
}

// splitDirSegments splits a path into its non-empty segments.
func splitDirSegments(dir string) []string {
	var out []string
	for _, s := range strings.Split(filepath.ToSlash(filepath.Clean(dir)), "/") {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// sharedDirSegments counts the leading path segments two directories share,
// comparing the way the host filesystem does: case-insensitively on macOS and
// Windows, exactly on Linux. Folding case everywhere would invent proximity on
// a case-sensitive filesystem, where /repo/UI and /repo/ui are genuinely
// different directories — and an invented tiebreak is exactly the silent guess
// this narrowing exists to avoid.
func sharedDirSegments(a, b string) int {
	as, bs := splitDirSegments(a), splitDirSegments(b)
	n := 0
	for n < len(as) && n < len(bs) && sameSegment(as[n], bs[n]) {
		n++
	}
	return n
}

// sameSegment compares one path segment under the host's path semantics.
func sameSegment(a, b string) bool {
	if caseInsensitiveFS {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// caseInsensitiveFS is whether the platform's default filesystem folds case.
var caseInsensitiveFS = runtime.GOOS == "darwin" || runtime.GOOS == "windows"

// devCommandFor resolves verb's argv for a target's ecosystem (node pm-detection
// keys off root).
func devCommandFor(t target, verb, root string) ([]string, bool) {
	return resolveVerbCommand(t.Eco, verb, root)
}

// resolveVerbCommand maps a verb to its argv for an ecosystem, with the .NET
// `format` verb routed through the configured/conventional formatter (CSharpier
// or `dotnet format`); everything else is the shared registry. Pure resolution
// (no install/prompt) so display, --all, info, and completion all agree — the
// run paths add the CSharpier preflight via requireDotnetFormatter.
func resolveVerbCommand(eco, verb, root string) ([]string, bool) {
	if eco == detect.DotNet && verb == "format" {
		if argv, ok := dotnetFormatArgv(root); ok {
			return argv, true
		}
	}
	return detect.CommandFor(eco, verb, root)
}
