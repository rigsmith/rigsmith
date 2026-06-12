package planner

import (
	"strings"
	"testing"

	"github.com/rigsmith/core/changeset"
	"github.com/rigsmith/core/config"
	"github.com/rigsmith/core/plugin"
)

func pkg(name, version string, deps ...string) plugin.Package {
	p := plugin.Package{Name: name, Version: version, ManifestPath: name + ".csproj"}
	for _, d := range deps {
		p.Dependencies = append(p.Dependencies, plugin.Dependency{Name: d})
	}
	return p
}

func cs(summary string, releases ...changeset.Release) *changeset.Changeset {
	return &changeset.Changeset{Summary: summary, Releases: releases}
}

func find(plan []*Module, name string) *Module {
	for _, m := range plan {
		if m.Name == name {
			return m
		}
	}
	return nil
}

func TestSimpleBump(t *testing.T) {
	pkgs := []plugin.Package{pkg("Core", "1.2.3")}
	changesets := []*changeset.Changeset{cs("a change", changeset.Release{Name: "Core", Bump: changeset.BumpMinor})}
	plan := Plan(changesets, pkgs, config.Default())

	m := find(plan, "Core")
	if m == nil {
		t.Fatal("Core missing from plan")
	}
	if got := m.NewVersion().String(); got != "1.3.0" {
		t.Errorf("NewVersion = %s, want 1.3.0", got)
	}
}

func TestCascadePatchesDependents(t *testing.T) {
	// App depends on Core; bumping Core should patch-bump App.
	pkgs := []plugin.Package{pkg("Core", "1.0.0"), pkg("App", "2.0.0", "Core")}
	changesets := []*changeset.Changeset{cs("core change", changeset.Release{Name: "Core", Bump: changeset.BumpMinor})}
	plan := Plan(changesets, pkgs, config.Default())

	app := find(plan, "App")
	if app == nil {
		t.Fatal("App should be in plan via cascade")
	}
	if got := app.NewVersion().String(); got != "2.0.1" {
		t.Errorf("App NewVersion = %s, want 2.0.1 (patch from dependency)", got)
	}
	if got := app.HighestBump(); got != changeset.BumpPatch {
		t.Errorf("App bump = %v, want patch", got)
	}
}

func TestTransitiveCascade(t *testing.T) {
	// Web -> App -> Core. Bumping Core patches both App and Web.
	pkgs := []plugin.Package{
		pkg("Core", "1.0.0"),
		pkg("App", "1.0.0", "Core"),
		pkg("Web", "1.0.0", "App"),
	}
	changesets := []*changeset.Changeset{cs("core", changeset.Release{Name: "Core", Bump: changeset.BumpMajor})}
	plan := Plan(changesets, pkgs, config.Default())

	if web := find(plan, "Web"); web == nil || web.NewVersion().String() != "1.0.1" {
		t.Errorf("Web should be patched transitively, got %v", web)
	}
}

func TestFixedGroupReleasesAllMembers(t *testing.T) {
	pkgs := []plugin.Package{pkg("A", "1.0.0"), pkg("B", "1.0.0")}
	cfg := config.Default()
	cfg.Fixed = [][]string{{"A", "B"}}
	changesets := []*changeset.Changeset{cs("a", changeset.Release{Name: "A", Bump: changeset.BumpMinor})}
	plan := Plan(changesets, pkgs, cfg)

	b := find(plan, "B")
	if b == nil {
		t.Fatal("B should be released by fixed group even with no changeset")
	}
	if got := b.NewVersion().String(); got != "1.1.0" {
		t.Errorf("B NewVersion = %s, want 1.1.0 (coordinated)", got)
	}
}

// pkgRanged builds a package with ranged dependencies of a given kind.
func pkgDep(name, version string, deps ...plugin.Dependency) plugin.Package {
	return plugin.Package{Name: name, Version: version, ManifestPath: name + "/package.json", Dependencies: deps}
}

func TestRangeAwareInRangeNoBump(t *testing.T) {
	// App depends on Core ^1.0.0. A Core minor (1.0.0→1.1.0) stays in range → App not released.
	pkgs := []plugin.Package{
		pkg("Core", "1.0.0"),
		pkgDep("App", "2.0.0", plugin.Dependency{Name: "Core", Kind: plugin.DepNormal, Range: "^1.0.0"}),
	}
	changesets := []*changeset.Changeset{cs("c", changeset.Release{Name: "Core", Bump: changeset.BumpMinor})}
	plan := Plan(changesets, pkgs, config.Default())
	if find(plan, "App") != nil {
		t.Error("App should NOT release: 1.1.0 satisfies ^1.0.0")
	}
}

func TestRangeAwareOutOfRangeBumps(t *testing.T) {
	// App depends on Core ^1.0.0. A Core major (1.0.0→2.0.0) is out of range → App patch-bumps.
	pkgs := []plugin.Package{
		pkg("Core", "1.0.0"),
		pkgDep("App", "2.0.0", plugin.Dependency{Name: "Core", Kind: plugin.DepNormal, Range: "^1.0.0"}),
	}
	changesets := []*changeset.Changeset{cs("c", changeset.Release{Name: "Core", Bump: changeset.BumpMajor})}
	plan := Plan(changesets, pkgs, config.Default())
	app := find(plan, "App")
	if app == nil || app.NewVersion().String() != "2.0.1" {
		t.Fatalf("App should patch-bump out of range, got %v", app)
	}
	// And its manifest dep range should be rewritten ^1.0.0 → ^2.0.0.
	if len(app.DepUpdates) != 1 || app.DepUpdates[0].NewVersion != "^2.0.0" {
		t.Errorf("DepUpdates = %+v, want [{Core ^2.0.0}]", app.DepUpdates)
	}
}

func TestUpdateInternalDependenciesMinor(t *testing.T) {
	pkgs := []plugin.Package{
		pkg("Core", "1.0.0"),
		pkgDep("App", "2.0.0", plugin.Dependency{Name: "Core", Kind: plugin.DepNormal, Range: "^1.0.0"}),
	}
	cfg := config.Default()
	cfg.UpdateInternalDependencies = config.UpdateMinor
	// Out of range (major) → dependent PATCH-bumps regardless of updateInternalDependencies.
	// Verified against @changesets: App goes 2.0.0 → 2.0.1 (not 2.1.0). updateInternalDependencies
	// is an in-range threshold, not the out-of-range bump level.
	changesets := []*changeset.Changeset{cs("c", changeset.Release{Name: "Core", Bump: changeset.BumpMajor})}
	plan := Plan(changesets, pkgs, cfg)
	if app := find(plan, "App"); app == nil || app.NewVersion().String() != "2.0.1" {
		t.Fatalf("App should patch-bump out of range, got %v", app)
	}
}

func TestPeerDependencyForcesMajor(t *testing.T) {
	// App peer-depends on Core ^1.0.0. A Core minor that's out of range → App major.
	pkgs := []plugin.Package{
		pkg("Core", "1.0.0"),
		pkgDep("App", "2.3.4", plugin.Dependency{Name: "Core", Kind: plugin.DepPeer, Range: "^1.0.0"}),
	}
	changesets := []*changeset.Changeset{cs("c", changeset.Release{Name: "Core", Bump: changeset.BumpMajor})}
	plan := Plan(changesets, pkgs, config.Default())
	if app := find(plan, "App"); app == nil || app.NewVersion().String() != "3.0.0" {
		t.Fatalf("peer dependent should major-bump, got %v", app)
	}
}

func TestDevDependencyNoRelease(t *testing.T) {
	// App dev-depends on Core (out of range). Dev deps don't bump the dependent,
	// but Node still rewrites the manifest range — a "none" release (RangeOnly):
	// version unchanged, no changelog, range ^1.0.0 → ^2.0.0.
	pkgs := []plugin.Package{
		pkg("Core", "1.0.0"),
		pkgDep("App", "2.0.0", plugin.Dependency{Name: "Core", Kind: plugin.DepDev, Range: "^1.0.0"}),
	}
	changesets := []*changeset.Changeset{cs("c", changeset.Release{Name: "Core", Bump: changeset.BumpMajor})}
	plan := Plan(changesets, pkgs, config.Default())
	app := find(plan, "App")
	if app == nil {
		t.Fatal("App should be in the plan as a none release (range rewrite only)")
	}
	if !app.RangeOnly || app.HighestBump() != changeset.BumpNone {
		t.Errorf("App should be RangeOnly with no bump, got RangeOnly=%v bump=%v", app.RangeOnly, app.HighestBump())
	}
	if got := app.NewVersion().String(); got != "2.0.0" {
		t.Errorf("App version should stay 2.0.0, got %s", got)
	}
	if len(app.DepUpdates) != 1 || app.DepUpdates[0].NewVersion != "^2.0.0" {
		t.Errorf("DepUpdates = %+v, want [{Core ^2.0.0}]", app.DepUpdates)
	}
	if depChangelogLine(app) != "" {
		t.Error("a none release must not carry changelog changes")
	}
}

func TestIgnoredDependentRangeOnly(t *testing.T) {
	// CoreBench (exact dep on Core) is ignored: not bumped, no changelog, but its
	// manifest range is still rewritten to Core's new version (Node-verified).
	pkgs := []plugin.Package{
		pkg("Core", "1.0.0"),
		pkgDep("CoreBench", "1.0.0", plugin.Dependency{Name: "Core", Kind: plugin.DepNormal, Range: "1.0.0"}),
	}
	cfg := config.Default()
	cfg.Ignore = []string{"CoreBench"}
	changesets := []*changeset.Changeset{cs("c", changeset.Release{Name: "Core", Bump: changeset.BumpMinor})}
	plan := Plan(changesets, pkgs, cfg)
	bench := find(plan, "CoreBench")
	if bench == nil {
		t.Fatal("CoreBench should be in the plan as a none release")
	}
	if !bench.RangeOnly || bench.NewVersion().String() != "1.0.0" {
		t.Errorf("CoreBench should be RangeOnly at 1.0.0, got RangeOnly=%v version=%s", bench.RangeOnly, bench.NewVersion())
	}
	if len(bench.DepUpdates) != 1 || bench.DepUpdates[0].NewVersion != "1.1.0" {
		t.Errorf("DepUpdates = %+v, want [{Core 1.1.0}]", bench.DepUpdates)
	}
	if len(bench.Changes) != 0 {
		t.Errorf("a none release must not carry changelog changes, got %+v", bench.Changes)
	}
}

// depChangelogLine returns the "Updated dependencies" change description, or "".
func depChangelogLine(m *Module) string {
	for _, c := range m.Changes {
		if len(c.Description) >= len(dependencyUpdatesHeader) && c.Description[:len(dependencyUpdatesHeader)] == dependencyUpdatesHeader {
			return c.Description
		}
	}
	return ""
}

func TestInRangeRewriteAtThreshold(t *testing.T) {
	// App (^1.0.0 on Core) releases for its own reasons; Core patch-bumps in
	// range. Threshold patch → range rewritten ^1.0.0→^1.0.1 + changelog line.
	// Verified against @changesets v3.0.0-next.5.
	pkgs := []plugin.Package{
		pkg("Core", "1.0.0"),
		pkgDep("App", "1.0.0", plugin.Dependency{Name: "Core", Kind: plugin.DepNormal, Range: "^1.0.0"}),
	}
	changesets := []*changeset.Changeset{
		cs("core fix", changeset.Release{Name: "Core", Bump: changeset.BumpPatch}),
		cs("app feature", changeset.Release{Name: "App", Bump: changeset.BumpMinor}),
	}
	plan := Plan(changesets, pkgs, config.Default())
	app := find(plan, "App")
	if app == nil {
		t.Fatal("App missing from plan")
	}
	if len(app.DepUpdates) != 1 || app.DepUpdates[0].NewVersion != "^1.0.1" {
		t.Errorf("DepUpdates = %+v, want [{Core ^1.0.1}]", app.DepUpdates)
	}
	if depChangelogLine(app) == "" {
		t.Error("App should have an Updated dependencies changelog line")
	}
}

func TestInRangeNoRewriteBelowThreshold(t *testing.T) {
	// Same as above with threshold minor: Core's patch bump is below it, so the
	// range stays ^1.0.0 AND no changelog line appears (Node-verified).
	pkgs := []plugin.Package{
		pkg("Core", "1.0.0"),
		pkgDep("App", "1.0.0", plugin.Dependency{Name: "Core", Kind: plugin.DepNormal, Range: "^1.0.0"}),
	}
	cfg := config.Default()
	cfg.UpdateInternalDependencies = config.UpdateMinor
	changesets := []*changeset.Changeset{
		cs("core fix", changeset.Release{Name: "Core", Bump: changeset.BumpPatch}),
		cs("app feature", changeset.Release{Name: "App", Bump: changeset.BumpMinor}),
	}
	plan := Plan(changesets, pkgs, cfg)
	app := find(plan, "App")
	if app == nil {
		t.Fatal("App missing from plan")
	}
	if len(app.DepUpdates) != 0 {
		t.Errorf("DepUpdates = %+v, want none (below in-range threshold)", app.DepUpdates)
	}
	if got := depChangelogLine(app); got != "" {
		t.Errorf("App should have no Updated dependencies line, got %q", got)
	}
}

func TestOutOfRangeRewriteBelowThreshold(t *testing.T) {
	// Exact range, Core patch, threshold minor: out-of-range always rewrites and
	// gets the changelog line, even below the threshold (Node-verified).
	pkgs := []plugin.Package{
		pkg("Core", "1.0.0"),
		pkgDep("App", "1.0.0", plugin.Dependency{Name: "Core", Kind: plugin.DepNormal, Range: "1.0.0"}),
	}
	cfg := config.Default()
	cfg.UpdateInternalDependencies = config.UpdateMinor
	changesets := []*changeset.Changeset{cs("core fix", changeset.Release{Name: "Core", Bump: changeset.BumpPatch})}
	plan := Plan(changesets, pkgs, cfg)
	app := find(plan, "App")
	if app == nil || app.NewVersion().String() != "1.0.1" {
		t.Fatalf("App should patch-bump, got %v", app)
	}
	if len(app.DepUpdates) != 1 || app.DepUpdates[0].NewVersion != "1.0.1" {
		t.Errorf("DepUpdates = %+v, want [{Core 1.0.1}]", app.DepUpdates)
	}
	if depChangelogLine(app) == "" {
		t.Error("App should have an Updated dependencies changelog line")
	}
}

func TestSnapshotRetargetsDeps(t *testing.T) {
	// ApplySnapshot pins dep rewrites and changelog lines to the exact snapshot
	// version, dropping the range operator (Node: ^1.0.0 → 0.0.0-canary-…).
	pkgs := []plugin.Package{
		pkg("Core", "1.0.0"),
		pkgDep("App", "1.0.0", plugin.Dependency{Name: "Core", Kind: plugin.DepNormal, Range: "^1.0.0"}),
	}
	changesets := []*changeset.Changeset{cs("breaking", changeset.Release{Name: "Core", Bump: changeset.BumpMajor})}
	plan := Plan(changesets, pkgs, config.Default())
	ApplySnapshot(plan, false, "canary-20240101000000")

	app := find(plan, "App")
	want := "0.0.0-canary-20240101000000"
	if app.ResolvedVersion() != want {
		t.Errorf("App snapshot version = %q, want %q", app.ResolvedVersion(), want)
	}
	if len(app.DepUpdates) != 1 || app.DepUpdates[0].NewVersion != want {
		t.Errorf("DepUpdates = %+v, want exact pin to %s", app.DepUpdates, want)
	}
	if line := depChangelogLine(app); !strings.Contains(line, "Core@"+want) {
		t.Errorf("dep changelog line = %q, want it to reference Core@%s", line, want)
	}
}

func TestPreRetargetsDeps(t *testing.T) {
	// ApplyPre re-renders dep rewrites with the prerelease version, preserving
	// the range operator (Node: ^1.0.0 → ^2.0.0-next.0).
	pkgs := []plugin.Package{
		pkg("Core", "1.0.0"),
		pkgDep("App", "1.0.0", plugin.Dependency{Name: "Core", Kind: plugin.DepNormal, Range: "^1.0.0"}),
	}
	changesets := []*changeset.Changeset{cs("breaking", changeset.Release{Name: "Core", Bump: changeset.BumpMajor})}
	plan := Plan(changesets, pkgs, config.Default())
	ApplyPre(plan, "next")

	app := find(plan, "App")
	if len(app.DepUpdates) != 1 || app.DepUpdates[0].NewVersion != "^2.0.0-next.0" {
		t.Errorf("DepUpdates = %+v, want [{Core ^2.0.0-next.0}]", app.DepUpdates)
	}
	if line := depChangelogLine(app); !strings.Contains(line, "Core@2.0.0-next.0") {
		t.Errorf("dep changelog line = %q, want it to reference Core@2.0.0-next.0", line)
	}
}

func TestTwoDependentsBothPatchBump(t *testing.T) {
	// App and Web both (ranglessly) depend on Core; a Core minor patches both.
	pkgs := []plugin.Package{
		pkg("Core", "1.0.0"),
		pkg("App", "1.0.0", "Core"),
		pkg("Web", "1.0.0", "Core"),
	}
	changesets := []*changeset.Changeset{cs("core change", changeset.Release{Name: "Core", Bump: changeset.BumpMinor})}
	plan := Plan(changesets, pkgs, config.Default())

	if len(plan) != 3 {
		t.Fatalf("plan should hold Core + both dependents, got %d modules", len(plan))
	}
	for _, name := range []string{"App", "Web"} {
		m := find(plan, name)
		if m == nil || m.NewVersion().String() != "1.0.1" {
			t.Errorf("%s should patch-bump to 1.0.1, got %v", name, m)
		}
	}
}

func TestTwoPatchesBumpOnce(t *testing.T) {
	// Two patch changesets on one package → one patch bump (1.0.0→1.0.1, not 1.0.2).
	pkgs := []plugin.Package{pkg("Core", "1.0.0")}
	changesets := []*changeset.Changeset{
		cs("first fix", changeset.Release{Name: "Core", Bump: changeset.BumpPatch}),
		cs("second fix", changeset.Release{Name: "Core", Bump: changeset.BumpPatch}),
	}
	plan := Plan(changesets, pkgs, config.Default())

	m := find(plan, "Core")
	if m == nil {
		t.Fatal("Core missing from plan")
	}
	if len(m.Changes) != 2 {
		t.Errorf("Changes = %d, want 2 (both changesets recorded)", len(m.Changes))
	}
	if got := m.NewVersion().String(); got != "1.0.1" {
		t.Errorf("NewVersion = %s, want 1.0.1 (a single patch bump)", got)
	}
}

func TestOneChangesetDifferentBumpPerName(t *testing.T) {
	// A single changeset naming two packages with different bumps applies each.
	pkgs := []plugin.Package{pkg("Core", "1.0.0"), pkg("App", "1.0.0")}
	changesets := []*changeset.Changeset{cs("a mixed change",
		changeset.Release{Name: "Core", Bump: changeset.BumpMinor},
		changeset.Release{Name: "App", Bump: changeset.BumpPatch},
	)}
	plan := Plan(changesets, pkgs, config.Default())

	if m := find(plan, "Core"); m == nil || m.NewVersion().String() != "1.1.0" {
		t.Errorf("Core should minor-bump to 1.1.0, got %v", m)
	}
	if m := find(plan, "App"); m == nil || m.NewVersion().String() != "1.0.1" {
		t.Errorf("App should patch-bump to 1.0.1, got %v", m)
	}
}

func TestLinkedGroupPartialRelease(t *testing.T) {
	// Linked: only the member with a changeset releases, coordinated onto the
	// group's highest current version (1.2.0) + minor. B is NOT released.
	pkgs := []plugin.Package{pkg("A", "1.0.0"), pkg("B", "1.2.0")}
	cfg := config.Default()
	cfg.Linked = [][]string{{"A", "B"}}
	changesets := []*changeset.Changeset{cs("a", changeset.Release{Name: "A", Bump: changeset.BumpMinor})}
	plan := Plan(changesets, pkgs, cfg)

	if find(plan, "B") != nil {
		t.Error("B should NOT release: linked (unlike fixed) only coordinates releasing members")
	}
	a := find(plan, "A")
	if a == nil {
		t.Fatal("A missing from plan")
	}
	if got := a.NewVersion().String(); got != "1.3.0" {
		t.Errorf("A NewVersion = %s, want 1.3.0 (coordinated to highest current version)", got)
	}
}

func TestLinkedGroupSharesHighestBumpAndVersion(t *testing.T) {
	// Linked, both releasing with different bumps: both share the highest bump
	// (minor) from the highest current version (1.2.0).
	pkgs := []plugin.Package{pkg("A", "1.0.0"), pkg("B", "1.2.0")}
	cfg := config.Default()
	cfg.Linked = [][]string{{"A", "B"}}
	changesets := []*changeset.Changeset{
		cs("a feature", changeset.Release{Name: "A", Bump: changeset.BumpMinor}),
		cs("b fix", changeset.Release{Name: "B", Bump: changeset.BumpPatch}),
	}
	plan := Plan(changesets, pkgs, cfg)

	if len(plan) != 2 {
		t.Fatalf("plan should hold both members, got %d", len(plan))
	}
	for _, name := range []string{"A", "B"} {
		if m := find(plan, name); m == nil || m.NewVersion().String() != "1.3.0" {
			t.Errorf("%s should coordinate to 1.3.0, got %v", name, m)
		}
	}
}

func TestLockstepSharedVersionFileCoordinatesToHighestBump(t *testing.T) {
	// Both packages inherit their version from the same Directory.Build.props, so
	// they move together to the highest bump: 1.0.0 becomes 1.1.0 for both.
	const props = "Directory.Build.props"
	pkgs := []plugin.Package{
		{Name: "A", Version: "1.0.0", ManifestPath: "A.csproj", VersionFile: props},
		{Name: "B", Version: "1.0.0", ManifestPath: "B.csproj", VersionFile: props},
	}
	changesets := []*changeset.Changeset{
		cs("a feature", changeset.Release{Name: "A", Bump: changeset.BumpMinor}),
		cs("b fix", changeset.Release{Name: "B", Bump: changeset.BumpPatch}),
	}
	plan := Plan(changesets, pkgs, config.Default())

	for _, name := range []string{"A", "B"} {
		m := find(plan, name)
		if m == nil {
			t.Fatalf("%s missing from plan", name)
		}
		if got := m.NewVersion().String(); got != "1.1.0" {
			t.Errorf("%s NewVersion = %s, want 1.1.0 (lockstep to highest bump)", name, got)
		}
		if got := m.EffectiveVersionFile(); got != props {
			t.Errorf("%s EffectiveVersionFile = %s, want %s", name, got, props)
		}
	}
}

func TestDisplayNameKeepsIdentity(t *testing.T) {
	// The package's identity (what changesets match against) is Name; the
	// changelog title uses DisplayName (the .NET PackageId).
	pkgs := []plugin.Package{{Name: "Core", DisplayName: "Acme.Core", Version: "1.0.0", ManifestPath: "Core.csproj"}}
	changesets := []*changeset.Changeset{cs("a change", changeset.Release{Name: "Core", Bump: changeset.BumpMinor})}
	plan := Plan(changesets, pkgs, config.Default())

	m := find(plan, "Core")
	if m == nil {
		t.Fatal("Core missing from plan (changesets match Name, not DisplayName)")
	}
	if m.DisplayName != "Acme.Core" {
		t.Errorf("DisplayName = %s, want Acme.Core", m.DisplayName)
	}
	if req := ModuleToRequest(m); req.Package.Name != "Core" || req.Package.DisplayName != "Acme.Core" {
		t.Errorf("changelog request = %s/%s, want Core/Acme.Core", req.Package.Name, req.Package.DisplayName)
	}
}

func TestDependentReferencesDisplayName(t *testing.T) {
	// A dependent's "Updated dependencies" line references the changed package by
	// its DisplayName (Acme.Core), not its identity (Core).
	pkgs := []plugin.Package{
		{Name: "Core", DisplayName: "Acme.Core", Version: "1.0.0", ManifestPath: "Core.csproj"},
		pkg("Web", "1.0.0", "Core"),
	}
	changesets := []*changeset.Changeset{cs("a change", changeset.Release{Name: "Core", Bump: changeset.BumpMinor})}
	plan := Plan(changesets, pkgs, config.Default())

	web := find(plan, "Web")
	if web == nil {
		t.Fatal("Web should be in plan via cascade")
	}
	want := dependencyUpdatesHeader + "\n  - Acme.Core@1.1.0"
	if got := depChangelogLine(web); got != want {
		t.Errorf("dep changelog line = %q, want %q", got, want)
	}
}

func TestIgnoreFiltersPackage(t *testing.T) {
	pkgs := []plugin.Package{pkg("Core", "1.0.0"), pkg("CoreBench", "1.0.0", "Core")}
	cfg := config.Default()
	cfg.Ignore = []string{"*Bench"}
	changesets := []*changeset.Changeset{cs("c", changeset.Release{Name: "Core", Bump: changeset.BumpPatch})}
	plan := Plan(changesets, pkgs, cfg)

	if find(plan, "CoreBench") != nil {
		t.Error("CoreBench should be filtered by ignore")
	}
}

func TestVersionStrategyLockstepVsIndependent(t *testing.T) {
	// A and B share a version file. A bumps minor, B bumps patch.
	pkgs := []plugin.Package{
		{Name: "A", Version: "1.0.0", ManifestPath: "A/A.csproj", VersionFile: "Directory.Build.props"},
		{Name: "B", Version: "1.0.0", ManifestPath: "B/B.csproj", VersionFile: "Directory.Build.props"},
	}
	changesets := []*changeset.Changeset{
		cs("a", changeset.Release{Name: "A", Bump: changeset.BumpMinor}),
		cs("b", changeset.Release{Name: "B", Bump: changeset.BumpPatch}),
	}

	// Lockstep (default): the shared version moves together — both → 1.1.0.
	lock := Plan(changesets, pkgs, config.Default())
	for _, n := range []string{"A", "B"} {
		m := find(lock, n)
		if m == nil {
			t.Fatalf("lockstep: %s missing", n)
		}
		if got := m.NewVersion().String(); got != "1.1.0" {
			t.Errorf("lockstep: %s = %s, want 1.1.0 (unified)", n, got)
		}
	}

	// Independent: each versions on its own changeset, written inline.
	cfg := config.Default()
	cfg.VersionStrategy = config.Independent
	ind := Plan(changesets, pkgs, cfg)
	a, b := find(ind, "A"), find(ind, "B")
	if a == nil || b == nil {
		t.Fatal("independent: A or B missing")
	}
	if got := a.NewVersion().String(); got != "1.1.0" {
		t.Errorf("independent: A = %s, want 1.1.0", got)
	}
	if got := b.NewVersion().String(); got != "1.0.1" {
		t.Errorf("independent: B = %s, want 1.0.1 (separate)", got)
	}
	if a.VersionFile != "" || b.VersionFile != "" {
		t.Errorf("independent: VersionFile should be cleared for inline writes, got A=%q B=%q", a.VersionFile, b.VersionFile)
	}
}
