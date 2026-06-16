package cli

import (
	"context"
	"io/fs"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rigsmith/rigsmith/core/ecosystem"
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
func discoverWorkspace(ctx context.Context, root string, exclude []string) []target {
	var out []target
	for _, eco := range ecosystem.Default().All() {
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
	return out
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

// dotnetTargets discovers the repo's .NET projects via the convention-first dev
// model (solution-aware, version-independent), carrying each project's runnable
// and test classification and its intra-repo project-reference dependencies.
// detect.DiscoverDotNet applies the exclude globs itself.
func dotnetTargets(root string, exclude []string) []target {
	cfg, _ := config.LoadMerged(root)
	var out []target
	for _, p := range detect.DiscoverDotNet(root, cfg.Solution, exclude) {
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
func topoSort(targets []target) []target {
	byName := make(map[string]target, len(targets))
	for _, t := range targets {
		byName[t.Name] = t
	}
	// indegree = number of (in-repo) deps not yet emitted.
	indeg := map[string]int{}
	dependents := map[string][]string{} // dep -> packages that depend on it
	for _, t := range targets {
		for _, d := range t.Deps {
			if _, ok := byName[d]; ok && d != t.Name {
				indeg[t.Name]++
				dependents[d] = append(dependents[d], t.Name)
			}
		}
	}

	var ready []string
	for _, t := range targets {
		if indeg[t.Name] == 0 {
			ready = append(ready, t.Name)
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
		order = append(order, byName[name])

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
		var rest []target
		for _, t := range targets {
			if !emitted[t.Name] {
				rest = append(rest, t)
			}
		}
		sort.Slice(rest, func(i, j int) bool { return rest[i].Name < rest[j].Name })
		order = append(order, rest...)
	}
	return order
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

// matchTarget resolves a query to a single package: exact (case-insensitive)
// match on name or short name wins; otherwise a unique substring match. Returns
// ok=false when there's no match or the substring match is ambiguous.
func matchTarget(targets []target, query string) (target, bool) {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return target{}, false
	}
	var subs []target
	for _, t := range targets {
		name, short := strings.ToLower(t.Name), strings.ToLower(t.shortName())
		if name == q || short == q {
			return t, true
		}
		if strings.Contains(name, q) || strings.Contains(short, q) {
			subs = append(subs, t)
		}
	}
	if len(subs) == 1 {
		return subs[0], true
	}
	return target{}, false
}

// matchDefaultProject resolves a configured defaultProject to a single target by
// EXACT (case-insensitive) match on full name, slash-short, or dot-short — and
// deliberately NOT by substring. A value like "Desktop" must scope to
// "Acme.Desktop" without going ambiguous against "Acme.Desktop.Tests" (which
// matchTarget's substring fallback would). Mirrors preferredRunTask, so the
// `rig watch run` default scoping agrees with the `rig run` path.
func matchDefaultProject(targets []target, defaultProject string) (target, bool) {
	q := strings.ToLower(strings.TrimSpace(defaultProject))
	if q == "" {
		return target{}, false
	}
	for _, t := range targets {
		if strings.ToLower(t.Name) == q ||
			strings.ToLower(t.shortName()) == q ||
			strings.ToLower(dotShortName(t.Name)) == q {
			return t, true
		}
	}
	return target{}, false
}

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
