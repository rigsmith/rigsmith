package planner

import (
	"context"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/config"
	"github.com/rigsmith/rigsmith/core/plugin"
)

// The scope becomes the bullet's lead-in and its sort key; the type decides the
// section. Together that is the whole point: features lead, each tool's lines
// sit together, and nobody has to name a file to get the order right.
func TestRenderGroupsByTypeAndLeadsWithScope(t *testing.T) {
	changes := []plugin.ChangelogChange{
		{Bump: "patch", Type: "fix", Scope: "rig", Summary: "fix(rig): a rig fix"},
		{Bump: "minor", Type: "feat", Scope: "rig", Summary: "feat(rig): a rig feature"},
		{Bump: "minor", Type: "feat", Scope: "clauderig", Summary: "feat(clauderig): a clauderig feature"},
		{Bump: "minor", Type: "feat", Summary: "feat: an unscoped feature"},
		{Bump: "patch", Type: "refactor", Summary: "refactor: an unscoped tidy"},
	}
	got := renderSections("1.1.0", "", changes, config.DefaultChangelogGroups, nil)

	// Sections in group order: enhancements, then fixes, then refactors.
	wantOrder := []string{"🚀 Enhancements", "🩹 Fixes", "💅 Refactors"}
	at := -1
	for _, section := range wantOrder {
		i := strings.Index(got, "### "+section)
		if i < 0 {
			t.Fatalf("missing section %q in:\n%s", section, got)
		}
		if i < at {
			t.Errorf("section %q is out of order in:\n%s", section, got)
		}
		at = i
	}
	// Scope leads the bullet, and the prefix is gone from the prose.
	if !strings.Contains(got, "- **clauderig:** a clauderig feature") {
		t.Errorf("scope lead-in missing in:\n%s", got)
	}
	if strings.Contains(got, "feat(clauderig):") {
		t.Errorf("conventional prefix left in the prose:\n%s", got)
	}
	// Within Enhancements, clauderig sorts before rig regardless of input order.
	enh := got[strings.Index(got, "### 🚀 Enhancements"):strings.Index(got, "### 🩹 Fixes")]
	if strings.Index(enh, "**clauderig:**") > strings.Index(enh, "**rig:**") {
		t.Errorf("bullets not grouped by scope:\n%s", enh)
	}
	// An unscoped bullet carries no lead-in, and sits after the scoped ones in
	// a section that holds both — the comparator branch that a section of only
	// scoped (or only unscoped) bullets never reaches.
	if !strings.Contains(got, "- an unscoped tidy") {
		t.Errorf("unscoped bullet should have no lead-in:\n%s", got)
	}
	if strings.Index(enh, "- an unscoped feature") < strings.LastIndex(enh, "**rig:**") {
		t.Errorf("an unscoped bullet should follow the scoped ones:\n%s", enh)
	}
}

// The configured scope order decides which tool leads inside a section;
// scopes left out of the list keep alphabetical order after the listed ones,
// and unscoped entries stay last.
func TestScopeOrderIsConfigurable(t *testing.T) {
	changes := []plugin.ChangelogChange{
		{Bump: "minor", Type: "feat", Scope: "clauderig", Summary: "feat(clauderig): c"},
		{Bump: "minor", Type: "feat", Summary: "feat: unscoped"},
		{Bump: "minor", Type: "feat", Scope: "shiprig", Summary: "feat(shiprig): s"},
		{Bump: "minor", Type: "feat", Scope: "rig", Summary: "feat(rig): r"},
	}
	got := renderSections("1.1.0", "", changes, config.DefaultChangelogGroups, []string{"rig", "clauderig"})

	want := []string{"**rig:** r", "**clauderig:** c", "**shiprig:** s", "- unscoped"}
	at := -1
	for _, w := range want {
		i := strings.Index(got, w)
		if i < 0 {
			t.Fatalf("missing %q in:\n%s", w, got)
		}
		if i < at {
			t.Errorf("%q out of order in:\n%s", w, got)
		}
		at = i
	}
}

// A summary that ends with newlines must not render indented blank lines under
// its bullet — every entry read out of a file ends that way.
func TestTrailingNewlinesDoNotBecomeBlankContinuations(t *testing.T) {
	got := renderSections("1.0.1", "", []plugin.ChangelogChange{
		{Bump: "patch", Type: "fix", Summary: "fix: a thing\n\n"},
	}, config.DefaultChangelogGroups, nil)
	if strings.Contains(got, "\n  \n") || strings.HasSuffix(got, "  \n") {
		t.Errorf("blank continuation line rendered:\n%q", got)
	}
}

// A summary that is only a conventional prefix, or only whitespace, has nothing
// to say — it must not render as a bare "- ".
func TestEmptySummariesRenderNoBullet(t *testing.T) {
	got := renderSections("1.0.1", "", []plugin.ChangelogChange{
		{Bump: "patch", Type: "fix", Scope: "rig", Summary: "fix(rig): "},
		{Bump: "patch", Type: "fix", Summary: "   \n\n"},
		{Bump: "patch", Type: "fix", Summary: "fix: a real one"},
	}, config.DefaultChangelogGroups, nil)

	if strings.Contains(got, "- \n") || strings.Contains(got, "- **rig:** \n") {
		t.Errorf("empty bullet rendered:\n%q", got)
	}
	if n := strings.Count(got, "\n- "); n != 1 {
		t.Errorf("bullets = %d, want 1 (only the real one):\n%s", n, got)
	}
}

// An entry with typed changes doesn't mix in bump headings: its untyped ones
// join the typed section their bump stands for.
func TestRenderSectionsFoldsUntypedChangesIntoTypedSections(t *testing.T) {
	got := renderSections("2.0.0", "", []plugin.ChangelogChange{
		{Bump: "patch", Summary: "Document exit codes", Type: "docs"},
		{Bump: "minor", Summary: "Add a --json flag"},
		{Bump: "major", Summary: "Drop Node 18"},
		{Bump: "patch", Summary: "Updated dependencies\n  - core@2.0.0\n  - ui@1.1.0", Dependencies: true},
	}, config.DefaultChangelogGroups, nil)
	for _, heading := range []string{"Minor Changes", "Patch Changes", "Major Changes"} {
		if strings.Contains(got, heading) {
			t.Errorf("a typed entry mixes in %q:\n%s", heading, got)
		}
	}
	for _, want := range []string{
		"### " + config.BreakingGroup.Section + "\n\n- Drop Node 18",
		"### " + sectionFor(t, "feat") + "\n\n- Add a --json flag",
		"### " + dependenciesSection + "\n\n- core@2.0.0\n- ui@1.1.0\n",
		"### " + sectionFor(t, "docs") + "\n\n- Document exit codes",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

// Without a typed change, the canon bump headings stay; and groups that name
// no feat/fix section fall back to them.
func TestRenderSectionsKeepsBumpHeadingsWhenUntypedOrUngrouped(t *testing.T) {
	untyped := renderSections("1.1.0", "", []plugin.ChangelogChange{
		{Bump: "minor", Summary: "Add a flag"},
		{Bump: "patch", Summary: "Fix a bug"},
	}, config.DefaultChangelogGroups, nil)
	if !strings.Contains(untyped, "### Minor Changes") || !strings.Contains(untyped, "### Patch Changes") {
		t.Errorf("untyped entry:\n%s", untyped)
	}
	ungrouped := renderSections("1.1.0", "", []plugin.ChangelogChange{
		{Bump: "patch", Summary: "Document it", Type: "docs"},
		{Bump: "minor", Summary: "Add a flag"},
	}, []config.ChangelogGroup{{Type: "docs", Section: "Docs", Bump: "patch"}}, nil)
	if !strings.Contains(ungrouped, "### Minor Changes\n\n- Add a flag") {
		t.Errorf("no feat group should fall back to the bump heading:\n%s", ungrouped)
	}
}

func sectionFor(t *testing.T, typ string) string {
	t.Helper()
	for _, g := range config.DefaultChangelogGroups {
		if g.Type == typ {
			return g.Section
		}
	}
	t.Fatalf("no default group for %s", typ)
	return ""
}

// The dependencies a request carries are in the order the built-in's merged
// entry lists them, by display name, which needn't follow the package names
// (.NET's can differ).
func TestDepReleasesFollowTheEntrysOrder(t *testing.T) {
	zeta := &Module{Name: "a.pkg", DisplayName: "Zeta", VersionOverride: "2.0.0"}
	alpha := &Module{Name: "b.pkg", DisplayName: "Alpha", VersionOverride: "3.0.0"}
	m := &Module{Name: "app", DisplayName: "app", depLinks: []depLink{{dep: zeta}, {dep: alpha}}}
	m.materializeDeps(false)

	var got []string
	for _, d := range m.DepReleases {
		got = append(got, d.DisplayName)
	}
	if strings.Join(got, ",") != "Alpha,Zeta" {
		t.Errorf("DepReleases = %v, want Alpha then Zeta", got)
	}
	if entry := RenderEntry(m); !strings.Contains(entry, "- Updated dependencies\n  - Alpha@3.0.0\n  - Zeta@2.0.0") {
		t.Errorf("entry:\n%s", entry)
	}
}

// A typed change whose summary is only a prefix renders nothing, so it
// doesn't switch the entry to typed sections; and a "none" change has no
// section, in either style.
func TestRenderSectionsIgnoresEmptyTypedAndNoneChanges(t *testing.T) {
	got := renderSections("1.0.1", "", []plugin.ChangelogChange{
		{Bump: "patch", Type: "fix", Summary: "fix:"},
		{Bump: "patch", Summary: "A fix"},
		{Bump: "none", Summary: "Nothing to release"},
	}, config.DefaultChangelogGroups, nil)
	if !strings.Contains(got, "### Patch Changes\n\n- A fix") {
		t.Errorf("an empty typed change restyled the entry:\n%s", got)
	}
	typed := renderSections("1.1.0", "", []plugin.ChangelogChange{
		{Bump: "minor", Type: "feat", Summary: "A feature"},
		{Bump: "none", Summary: "Nothing to release"},
	}, config.DefaultChangelogGroups, nil)
	for _, entry := range []string{got, typed} {
		if strings.Contains(entry, "None Changes") || strings.Contains(entry, "Nothing to release") {
			t.Errorf("a none change was rendered:\n%s", entry)
		}
	}
}

// Dependencies that render the same text are ordered by name, so the
// request's bytes don't depend on the manifest's order.
func TestDepReleasesBreakTiesByName(t *testing.T) {
	for _, order := range [][2]string{{"a.pkg", "b.pkg"}, {"b.pkg", "a.pkg"}} {
		one := &Module{Name: order[0], DisplayName: "Same", VersionOverride: "2.0.0"}
		two := &Module{Name: order[1], DisplayName: "Same", VersionOverride: "2.0.0"}
		m := &Module{Name: "app", DisplayName: "app", depLinks: []depLink{{dep: one}, {dep: two}}}
		m.materializeDeps(false)
		if m.DepReleases[0].Name != "a.pkg" || m.DepReleases[1].Name != "b.pkg" {
			t.Errorf("manifest order %v: DepReleases = %+v", order, m.DepReleases)
		}
	}
}

// The dependencies section comes last and holds nothing else: no 🩹 Fixes
// section appears just for them; and an entry with no typed changes keeps
// @changesets' "Updated dependencies" under Patch Changes.
func TestRenderSectionsGivesTypedDependenciesTheirOwnSection(t *testing.T) {
	deps := plugin.ChangelogChange{Bump: "patch", Summary: "Updated dependencies\n  - core@2.0.0", Dependencies: true}
	typed := renderSections("1.1.0", "", []plugin.ChangelogChange{
		{Bump: "minor", Type: "feat", Summary: "A feature"},
		deps,
		{Bump: "patch", Type: "zzz", Summary: "An unusual type"},
	}, config.DefaultChangelogGroups, nil)
	if strings.Contains(typed, sectionFor(t, "fix")) || strings.Contains(typed, "Updated dependencies") {
		t.Errorf("typed entry:\n%s", typed)
	}
	// After the configured groups, before sections for types no group names.
	feat, depsAt, other := strings.Index(typed, sectionFor(t, "feat")), strings.Index(typed, "### "+dependenciesSection+"\n\n- core@2.0.0\n"), strings.Index(typed, "### Zzz")
	if feat < 0 || depsAt < 0 || other < 0 || !(feat < depsAt && depsAt < other) {
		t.Errorf("section order (feat %d, dependencies %d, Zzz %d):\n%s", feat, depsAt, other, typed)
	}
	untyped := renderSections("1.0.1", "", []plugin.ChangelogChange{{Bump: "patch", Summary: "A fix"}, deps}, config.DefaultChangelogGroups, nil)
	if !strings.Contains(untyped, "### Patch Changes\n\n- A fix\n- Updated dependencies\n  - core@2.0.0") {
		t.Errorf("untyped entry:\n%s", untyped)
	}
}

// Through the built-in generator, as the engine calls it: a typed change and
// released dependencies render the dependencies in their own section.
func TestBuiltinRendersTypedDependenciesInTheirOwnSection(t *testing.T) {
	got, err := NewBuiltinGenerator(nil).Render(context.Background(), plugin.ChangelogRequest{
		Package: plugin.ChangelogPackage{NewVersion: "1.1.0"},
		Changes: []plugin.ChangelogChange{
			{Bump: "minor", Type: "feat", Summary: "A feature"},
			{Bump: "patch", Summary: "Updated dependencies\n  - core@2.0.0", Dependencies: true},
		},
		DependencyUpdates: []plugin.DependencyUpdate{{Name: "core", DisplayName: "core", NewVersion: "2.0.0"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "### "+dependenciesSection+"\n\n- core@2.0.0\n") || strings.Contains(got, sectionFor(t, "fix")) {
		t.Errorf("entry:\n%s", got)
	}
}

// A changelog group headed like the dependencies section keeps its own
// bucket and rank: its changes never join the dependency list.
func TestRenderSectionsKeepsAGroupNamedLikeTheDependencies(t *testing.T) {
	groups := []config.ChangelogGroup{
		{Type: "deps", Section: dependenciesSection, Bump: "patch"},
		{Type: "feat", Section: "Features", Bump: "minor"},
	}
	got := renderSections("1.1.0", "", []plugin.ChangelogChange{
		{Bump: "minor", Type: "feat", Summary: "A feature"},
		{Bump: "patch", Type: "deps", Summary: "Pin the toolchain"},
		{Bump: "patch", Summary: "Updated dependencies\n  - core@2.0.0", Dependencies: true},
	}, groups, nil)
	group := "### " + dependenciesSection + "\n\n- Pin the toolchain\n"
	list := "### " + dependenciesSection + "\n\n- core@2.0.0\n"
	if !strings.Contains(got, group) || !strings.Contains(got, list) {
		t.Errorf("want the group and the dependency list apart:\n%s", got)
	}
	// The group keeps its configured rank, ahead of Features.
	if strings.Index(got, group) > strings.Index(got, "### Features") {
		t.Errorf("the group lost its rank:\n%s", got)
	}
}

// An untyped entry's headings never name a bigger bump than the release
// makes (bumpMinorPreMajor's major on 0.x releases as a minor); a typed entry
// keeps a major change under Breaking, since the heading names the change.
func TestRenderSectionsCapsBumpHeadingsAtTheReleaseBump(t *testing.T) {
	untyped := renderSections("0.4.0", "minor", []plugin.ChangelogChange{
		{Bump: "major", Summary: "Break it"},
		{Bump: "patch", Summary: "Fix it"},
	}, config.DefaultChangelogGroups, nil)
	if want := "## 0.4.0\n\n### Minor Changes\n\n- Break it\n\n### Patch Changes\n\n- Fix it\n"; untyped != want {
		t.Errorf("untyped:\n%s\nwant:\n%s", untyped, want)
	}
	typed := renderSections("0.4.0", "minor", []plugin.ChangelogChange{
		{Bump: "major", Summary: "Break it"},
		{Bump: "minor", Type: "feat", Summary: "Add it"},
	}, config.DefaultChangelogGroups, nil)
	if !strings.Contains(typed, config.BreakingGroup.Section+"\n\n- Break it") {
		t.Errorf("typed: the major change left the Breaking section:\n%s", typed)
	}
}
