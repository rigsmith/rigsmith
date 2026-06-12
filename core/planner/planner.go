// Package planner computes a release plan from changesets and discovered
// packages: the version each package moves to, the cascade to its dependents,
// and the grouping rules (linked / fixed / lockstep). It is a faithful,
// ecosystem-agnostic port of net-changesets' ChangelogGenerator.cs, extended
// with the range-aware cascade from @changesets' assemble-release-plan.
//
// The cascade is range-aware: a dependent is released when a dependency's NEW
// version falls OUT of the declared version range. A rangeless reference (.NET
// ProjectReference, a Go require resolved by tag) is always treated as out of
// range, so it always cascades — preserving the original behavior. An
// out-of-range dependent always patch-bumps; a peer dependency on a minor/major
// release forces a major; devDependencies update the range only.
// updateInternalDependencies is solely the in-range range-rewrite threshold.
package planner

import (
	"fmt"
	"sort"
	"strings"

	"github.com/rigsmith/core/changeset"
	"github.com/rigsmith/core/config"
	"github.com/rigsmith/core/plugin"
	"github.com/rigsmith/core/semver"
)

const dependencyUpdatesHeader = "Updated dependencies"

// Change is one line of a package's changelog: a rendered description, the bump
// it carries, and its conventional type (for type-grouped changelogs).
type Change struct {
	Description string
	Bump        changeset.Bump
	Type        string // conventional type (feat/fix/…), empty when untyped
	Breaking    bool   // a `!` breaking change
}

// Module is the per-package release entry — the generalized ModuleChangelog.
type Module struct {
	Name         string
	DisplayName  string
	ManifestPath string
	VersionFile  string // shared version file, empty when inline
	Current      semver.Version
	Changes      []Change
	// DepUpdates are intra-repo dependency-range rewrites to apply to this
	// package's manifest (e.g. "^1.2.0" → "^1.3.0"). Empty for rangeless
	// ecosystems (.NET ProjectReference, Go require resolved by tag).
	DepUpdates []plugin.DependencyUpdate
	// VersionOverride, when set, is the literal version string written instead of
	// the stable bump — used by prerelease and snapshot runs whose versions carry
	// a suffix the stable bump can't express (e.g. 1.1.0-next.0, 0.0.0-canary-…).
	VersionOverride string
	// RangeOnly marks a @changesets "none"-type release: the package's version
	// does not change and no changelog is written, but its manifest dependency
	// ranges are rewritten. Produced for an ignored dependent and for a
	// dev-dependent of an out-of-range release. A RangeOnly module always has
	// HighestBump() == BumpNone, so NewVersion() is the current version.
	RangeOnly bool
	// depLinks are the releasing in-repo dependencies that passed the
	// updateInternalDependencies gate; Changes/DepUpdates are materialized from
	// them so prerelease/snapshot overrides can re-render with final versions.
	depLinks []depLink
	// cascadeBump is the bump determined for this package by the dependency
	// cascade (range-aware), independent of any direct changeset changes.
	cascadeBump changeset.Bump
	// bumpOverride, when set, coordinates the version of packages in a
	// fixed/linked/lockstep group.
	bumpOverride    changeset.Bump
	hasBumpOverride bool
}

// EffectiveVersionFile is where the bump is written: VersionFile when set, else
// the manifest.
func (m *Module) EffectiveVersionFile() string {
	if m.VersionFile != "" {
		return m.VersionFile
	}
	return m.ManifestPath
}

// HighestBump is the override if present, otherwise the max of the direct
// changes and the cascade-determined bump.
func (m *Module) HighestBump() changeset.Bump {
	if m.hasBumpOverride {
		return m.bumpOverride
	}
	highest := m.cascadeBump
	for _, c := range m.Changes {
		highest = highest.Max(c.Bump)
	}
	return highest
}

// NewVersion is the stable version this module bumps to.
func (m *Module) NewVersion() semver.Version {
	switch m.HighestBump() {
	case changeset.BumpMajor:
		return m.Current.RaiseMajor()
	case changeset.BumpMinor:
		return m.Current.RaiseMinor()
	case changeset.BumpPatch:
		return m.Current.RaisePatch()
	default:
		return m.Current
	}
}

// ResolvedVersion is the literal version string written for this release:
// VersionOverride when set (prerelease/snapshot), otherwise the stable bump.
func (m *Module) ResolvedVersion() string {
	if m.VersionOverride != "" {
		return m.VersionOverride
	}
	return m.NewVersion().String()
}

// depEdge is a "package X depends on package D" edge, from D's point of view.
type depEdge struct {
	dependent string                // the package that declares the dependency
	kind      plugin.DependencyKind // normal / dev / peer
	rng       string                // the declared version range ("" when rangeless)
}

// depLink is a releasing in-repo dependency of a releasing module, recorded so
// the "Updated dependencies" changelog line and the manifest range rewrite can
// be (re-)materialized after a version override (pre/snapshot) is applied.
type depLink struct {
	dep *Module
	rng string // the declared range ("" when rangeless → no manifest rewrite)
	dev bool   // devDependency: range rewritten, no changelog line
}

// Plan computes the release plan for the given changesets and packages.
func Plan(changesets []*changeset.Changeset, packages []plugin.Package, cfg *config.Config) []*Module {
	byName := make(map[string]plugin.Package, len(packages))
	for _, p := range packages {
		byName[p.Name] = p
	}

	// 1. Direct releases from the changesets.
	directOrder := generateModules(changesets, byName, cfg.Groups())
	rel := map[string]*Module{}
	order := make([]string, 0, len(directOrder))
	for _, m := range directOrder {
		rel[m.Name] = m
		order = append(order, m.Name)
	}

	// dependentsOf[D] = every package that declares a dependency on D.
	dependentsOf := map[string][]depEdge{}
	for _, p := range packages {
		for _, d := range p.Dependencies {
			dependentsOf[d.Name] = append(dependentsOf[d.Name], depEdge{dependent: p.Name, kind: d.Kind, rng: d.Range})
		}
	}

	// 2. Range-aware cascade to a fixpoint (port of @changesets determineDependents):
	// a dependent is released when a dependency's NEW version falls out of the
	// declared range (a rangeless reference is always "out of range"). An out-of-range
	// dependent is always PATCH-bumped — verified against @changesets, which patch-bumps
	// the dependent (and rewrites its range) regardless of the dependency's own bump size
	// or updateInternalDependencies. A peer dependency on a minor/major release forces the
	// dependent to major; devDependencies update the range but do not cause a release.
	//
	// Release decisions match @changesets and net-changesets exactly (verified against the
	// parity corpus + live Node): in-range dependents are never released; out-of-range ones
	// patch-bump. config.UpdateInternalDependencies does NOT affect release decisions — it is
	// only the threshold for rewriting an *in-range* dependency's range spec on a dependent
	// that is already releasing for its own reasons (modeled in section 3 below).
	//
	// The cascade and the fixed/linked/lockstep group coordination iterate TOGETHER to a
	// fixpoint (port of @changesets assembleReleasePlan, which loops determineDependents +
	// matchFixedConstraint until stable): a group pull can move a member out of a
	// dependent's range, which must cascade in turn. Node-verified: a dependent of a
	// fixed-pulled package patch-bumps with its range rewritten, exactly as if the member
	// had its own changeset. (net-changesets does NOT re-cascade after coordination — a
	// known net divergence; Go follows Node.)
	for {
		changed := false
		for _, name := range order {
			m := rel[name]
			rb := m.HighestBump()
			if rb == changeset.BumpNone {
				continue
			}
			newVer := m.NewVersion().String()
			for _, e := range dependentsOf[name] {
				inRange := e.rng != "" && semver.SatisfiesString(newVer, e.rng)
				cand := changeset.BumpNone
				switch {
				case e.kind == plugin.DepPeer && (rb == changeset.BumpMinor || rb == changeset.BumpMajor) && !inRange:
					cand = changeset.BumpMajor
				case e.kind == plugin.DepDev:
					// dev dependency out of range: a "none" release — the dependent's
					// version doesn't change but its manifest range is rewritten, so it
					// must enter the plan (Node-verified). In range: nothing.
					if !inRange {
						if _, ok := rel[e.dependent]; !ok {
							if pkg, found := byName[e.dependent]; found {
								rel[e.dependent] = newModule(pkg)
								order = append(order, e.dependent)
								changed = true
							}
						}
					}
				default: // normal / peer (patch) / build
					if !inRange {
						cand = changeset.BumpPatch
					}
				}
				if cand == changeset.BumpNone {
					continue
				}
				dm, ok := rel[e.dependent]
				if !ok {
					if pkg, found := byName[e.dependent]; found {
						dm = newModule(pkg)
						rel[e.dependent] = dm
						order = append(order, e.dependent)
					} else {
						continue
					}
				}
				if cand > dm.cascadeBump {
					dm.cascadeBump = cand
					changed = true
				}
			}
		}
		if !changed && !coordinateGroups(rel, &order, byName, packages, cfg) {
			break
		}
	}

	// 3. For each releasing package, record the releasing in-repo deps that pass
	// the updateInternalDependencies gate, then materialize the "Updated
	// dependencies" changelog lines and the manifest range rewrites from them.
	//
	// Gate (verified against live @changesets v3.0.0-next.5): an out-of-range or
	// rangeless dep always gets the line + rewrite (even below the threshold); an
	// in-range dep gets them only when its bump ≥ updateInternalDependencies —
	// below the threshold the range is left alone AND no changelog line appears.
	threshold, ok := changeset.ParseBump(string(cfg.UpdateInternalDependencies))
	if !ok || threshold == changeset.BumpNone {
		threshold = changeset.BumpPatch
	}
	for _, name := range order {
		m := rel[name]
		releasing := m.HighestBump() != changeset.BumpNone
		for _, d := range byName[name].Dependencies {
			dep, ok := rel[d.Name]
			if !ok || dep.HighestBump() == changeset.BumpNone {
				continue
			}
			inRange := d.Range != "" && semver.SatisfiesString(dep.NewVersion().String(), d.Range)
			// A non-releasing module is in rel only via a dev edge; it gets just
			// its out-of-range rewrites (an in-range dep never creates a release).
			if inRange && (!releasing || dep.HighestBump() < threshold) {
				continue
			}
			m.depLinks = append(m.depLinks, depLink{dep: dep, rng: d.Range, dev: d.Kind == plugin.DepDev})
		}
		m.materializeDeps(false)
	}

	// 4. Assemble the plan (grouping already coordinated in the fixpoint above).
	// A module with no bump but pending range rewrites (dev-dependent) rides
	// along as a "none" release.
	plan := make([]*Module, 0, len(order))
	for _, name := range order {
		m := rel[name]
		switch {
		case m.HighestBump() != changeset.BumpNone:
			plan = append(plan, m)
		case len(m.DepUpdates) > 0:
			m.RangeOnly = true
			plan = append(plan, m)
		}
	}

	// An ignored package is not released, but Node still rewrites its manifest
	// dependency ranges (a "none" release) — demote rather than drop it.
	out := plan[:0]
	for _, m := range plan {
		switch {
		case !cfg.IsIgnored(m.Name):
			out = append(out, m)
		case len(m.DepUpdates) > 0:
			m.RangeOnly = true
			m.cascadeBump = changeset.BumpNone
			m.Changes = nil
			out = append(out, m)
		}
	}

	// Independent strategy: write each package's version inline into its own
	// manifest, overriding any shared version file (SetVersion targets the
	// manifest when VersionFile is empty).
	if cfg != nil && cfg.VersionStrategy == config.Independent {
		for _, m := range out {
			m.VersionFile = ""
		}
	}
	return out
}

// rewriteRange replaces the version in a dependency range with newVersion,
// preserving the leading operator (^, ~, >=, etc.) and any workspace: prefix.
func rewriteRange(oldRange, newVersion string) string {
	prefix := ""
	r := oldRange
	if strings.HasPrefix(r, "workspace:") {
		prefix = "workspace:"
		r = strings.TrimPrefix(r, "workspace:")
	}
	// A workspace:* / * range carries no concrete version to rewrite.
	if r == "" || r == "*" {
		return oldRange
	}
	op := ""
	for _, o := range []string{">=", "<=", "^", "~", ">", "<", "="} {
		if strings.HasPrefix(r, o) {
			op = o
			break
		}
	}
	return prefix + op + newVersion
}

// generateModules creates one Module per directly-named package, merging the
// changes from every changeset that names it. Each change's bump is the
// changeset's explicit per-package bump when given, otherwise derived from the
// changeset's conventional type (an explicit bump is the override).
func generateModules(changesets []*changeset.Changeset, byName map[string]plugin.Package, groups []config.ChangelogGroup) []*Module {
	order := []string{}
	index := map[string]*Module{}

	for _, cs := range changesets {
		desc := cs.Summary // TODO: route through the changelog generator (git/github enrichment)
		typ, breaking, hasType := cs.EffectiveType()
		for _, rel := range cs.Releases {
			m, ok := index[rel.Name]
			if !ok {
				pkg, found := byName[rel.Name]
				if !found {
					continue // names a package this ecosystem doesn't own (interop)
				}
				m = newModule(pkg)
				index[rel.Name] = m
				order = append(order, rel.Name)
			}
			// Explicit per-package bump wins; otherwise derive from the type.
			bump := rel.Bump
			if bump == changeset.BumpNone && hasType {
				bump = deriveBump(typ, breaking, groups)
			}
			m.Changes = append(m.Changes, Change{Description: desc, Bump: bump, Type: typ, Breaking: breaking})
		}
	}

	out := make([]*Module, 0, len(order))
	for _, name := range order {
		out = append(out, index[name])
	}
	return out
}

// deriveBump maps a conventional type (and breaking flag) to a version bump via
// the configured groups. Breaking always wins (major); an unknown type defaults
// to patch.
func deriveBump(typ string, breaking bool, groups []config.ChangelogGroup) changeset.Bump {
	if breaking {
		return changeset.BumpMajor
	}
	for _, g := range groups {
		if g.Type == typ {
			b, _ := changeset.ParseBump(g.Bump)
			return b
		}
	}
	return changeset.BumpPatch
}

func newModule(p plugin.Package) *Module {
	v, _ := semver.Parse(p.Version)
	display := p.DisplayName
	if display == "" {
		display = p.Name
	}
	return &Module{
		Name:         p.Name,
		DisplayName:  display,
		ManifestPath: p.ManifestPath,
		VersionFile:  p.VersionFile,
		Current:      v,
	}
}

// materializeDeps (re-)builds the module's "Updated dependencies" changelog
// changes and manifest DepUpdates from its recorded depLinks, using each dep's
// ResolvedVersion so pre/snapshot overrides flow through. With exact set
// (snapshot mode, matching @changesets), the manifest range is pinned to the
// bare version instead of preserving the range operator.
func (m *Module) materializeDeps(exact bool) {
	changes := m.Changes[:0]
	for _, c := range m.Changes {
		if !strings.HasPrefix(c.Description, dependencyUpdatesHeader) {
			changes = append(changes, c)
		}
	}
	m.DepUpdates = nil
	for _, l := range m.depLinks {
		ver := l.dep.ResolvedVersion()
		if !l.dev && !m.RangeOnly {
			changes = append(changes, Change{
				Description: fmt.Sprintf("%s\n  - %s@%s", dependencyUpdatesHeader, l.dep.DisplayName, ver),
				Bump:        changeset.BumpPatch,
			})
		}
		if l.rng != "" {
			nv := rewriteRange(l.rng, ver)
			if exact {
				nv = ver
			}
			m.DepUpdates = append(m.DepUpdates, plugin.DependencyUpdate{Name: l.dep.Name, NewVersion: nv})
		}
	}
	m.Changes = mergeDependencyUpdates(changes)
}

// mergeDependencyUpdates collapses multiple "Updated dependencies" changes into
// one, preserving the C# ordering (ordinal by description).
func mergeDependencyUpdates(changes []Change) []Change {
	var deps []Change
	var rest []Change
	for _, c := range changes {
		if strings.HasPrefix(c.Description, dependencyUpdatesHeader) {
			deps = append(deps, c)
		} else {
			rest = append(rest, c)
		}
	}
	if len(deps) <= 1 {
		return changes
	}
	sort.Slice(deps, func(i, j int) bool { return deps[i].Description < deps[j].Description })
	var nested strings.Builder
	for _, d := range deps {
		nested.WriteString(strings.TrimPrefix(d.Description, dependencyUpdatesHeader))
	}
	return append(rest, Change{Description: dependencyUpdatesHeader + nested.String(), Bump: changeset.BumpPatch})
}

// coordinate forces a module to a shared version+bump (group coordination).
func coordinate(m *Module, version semver.Version, bump changeset.Bump) {
	m.Current = version
	m.bumpOverride = bump
	m.hasBumpOverride = true
}

// coordinateGroups applies linked/fixed/lockstep coordination to the working
// release set, adding missing fixed-group members. It runs inside the cascade
// fixpoint (see Plan section 2) and reports whether anything changed so the
// dependent cascade re-runs over the coordinated versions.
func coordinateGroups(rel map[string]*Module, order *[]string, byName map[string]plugin.Package, packages []plugin.Package, cfg *config.Config) bool {
	changed := false

	releasingIn := func(names []string) []*Module {
		var out []*Module
		for _, n := range names {
			if m, ok := rel[n]; ok && m.HighestBump() != changeset.BumpNone {
				out = append(out, m)
			}
		}
		return out
	}
	apply := func(m *Module, version semver.Version, bump changeset.Bump) {
		if m.hasBumpOverride && m.bumpOverride == bump && semver.Compare(m.Current, version) == 0 {
			return // already coordinated — keeps the fixpoint terminating
		}
		coordinate(m, version, bump)
		changed = true
	}

	// Linked: only the members already releasing coordinate.
	for _, grp := range cfg.Linked {
		releasing := releasingIn(grp)
		if len(releasing) == 0 {
			continue
		}
		bump := highestBump(releasing)
		version := highestCurrentVersion(grp, byName)
		for _, m := range releasing {
			apply(m, version, bump)
		}
	}

	// Fixed: coordinate AND add the non-releasing members.
	for _, grp := range cfg.Fixed {
		releasing := releasingIn(grp)
		if len(releasing) == 0 {
			continue
		}
		bump := highestBump(releasing)
		version := highestCurrentVersion(grp, byName)
		for _, member := range grp {
			if cfg.IsIgnored(member) {
				continue
			}
			if m, ok := rel[member]; ok {
				apply(m, version, bump)
				continue
			}
			if pkg, ok := byName[member]; ok {
				m := newModule(pkg)
				rel[member] = m
				*order = append(*order, member)
				apply(m, version, bump)
			}
		}
	}

	// Lockstep: packages sharing a version file move together — unless the
	// strategy is independent, in which case each versions on its own changesets
	// (Plan writes those inline; see the VersionFile clearing below).
	if cfg != nil && cfg.VersionStrategy == config.Independent {
		return changed
	}
	lockstep := map[string][]string{}
	for _, p := range packages {
		if p.VersionFile != "" {
			lockstep[p.VersionFile] = append(lockstep[p.VersionFile], p.Name)
		}
	}
	for _, names := range lockstep {
		releasing := releasingIn(names)
		if len(releasing) <= 1 {
			continue
		}
		bump := highestBump(releasing)
		version := highestCurrentVersion(names, byName)
		for _, m := range releasing {
			apply(m, version, bump)
		}
	}
	return changed
}

func findModule(plan []*Module, name string) *Module {
	for _, m := range plan {
		if m.Name == name {
			return m
		}
	}
	return nil
}

func highestBump(modules []*Module) changeset.Bump {
	highest := changeset.BumpNone
	for _, m := range modules {
		highest = highest.Max(m.HighestBump())
	}
	return highest
}

// highestCurrentVersion returns the highest current version among the named
// packages (prerelease-aware), so a coordinated group moves from one base.
func highestCurrentVersion(names []string, byName map[string]plugin.Package) semver.Version {
	var highest semver.Version
	set := map[string]bool{}
	for _, n := range names {
		set[n] = true
	}
	first := true
	for name := range set {
		p, ok := byName[name]
		if !ok {
			continue
		}
		v, _ := semver.Parse(p.Version)
		if first || semver.Compare(v, highest) > 0 {
			highest = v
			first = false
		}
	}
	return highest
}
