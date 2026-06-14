package planner

import (
	"fmt"
	"sort"
	"strings"

	"github.com/rigsmith/rigsmith/core/changeset"
	"github.com/rigsmith/rigsmith/core/config"
	"github.com/rigsmith/rigsmith/core/plugin"
)

// RenderEntry renders a module's release entry — the "## <version>" block with
// its sections — excluding the "# Title" file header, using the default groups.
//
// Dogfooding (per PLUGIN-PROTOCOL.md): RenderEntry does NOT have its own
// rendering path. It builds the exact plugin.ChangelogRequest a subprocess
// plugin would receive and routes it through the in-process built-in generator.
func RenderEntry(m *Module) string {
	out, _ := NewBuiltinGenerator(nil).Render(nil, ModuleToRequest(m))
	return out
}

// ModuleToRequest builds the changelog plugin request for a module. This is the
// exact object serialized to a subprocess generator's stdin.
func ModuleToRequest(m *Module) plugin.ChangelogRequest {
	changes := make([]plugin.ChangelogChange, 0, len(m.Changes))
	for _, c := range m.Changes {
		changes = append(changes, plugin.ChangelogChange{
			Bump:     c.Bump.String(),
			Summary:  c.Description,
			Type:     c.Type,
			Breaking: c.Breaking,
		})
	}
	return plugin.ChangelogRequest{
		APIVersion: plugin.APIVersion,
		Package: plugin.ChangelogPackage{
			Name:           m.Name,
			DisplayName:    m.DisplayName,
			CurrentVersion: m.Current.String(),
			NewVersion:     m.ResolvedVersion(),
		},
		Bump:                m.HighestBump().String(),
		Changes:             changes,
		Contributors:        m.Contributors,
		ContributorsSection: m.ContributorsSection,
	}
}

// renderContributors renders the trailing "Contributors" section (changelogen
// style): one bullet per author, linked to their GitHub page when a login was
// resolved, bare name otherwise. Emails are never rendered. Returns "" when
// there are no contributors. The leading blank line separates it from the
// change sections, matching the inter-section spacing.
func renderContributors(authors []plugin.Author, section string) string {
	if len(authors) == 0 {
		return ""
	}
	if strings.TrimSpace(section) == "" {
		section = config.DefaultContributorsSection
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n### %s\n\n", section)
	for _, a := range authors {
		name := a.Name
		if name == "" {
			name = a.Login
		}
		if a.Login != "" {
			fmt.Fprintf(&b, "- %s ([@%s](https://github.com/%s))\n", name, a.Login, a.Login)
		} else {
			fmt.Fprintf(&b, "- %s\n", name)
		}
	}
	return b.String()
}

// renderSections renders the "## <version>" header and the change sections.
//
// A change's section is: the "Breaking Changes" section if it is breaking; else
// the section for its conventional type (from groups); else the bump-based
// section ("Major/Minor/Patch Changes"). Sections are ordered: Breaking, then
// the configured group order, then Major, Minor, Patch — so an untyped changelog
// is byte-identical to the bump-only layout.
func renderSections(newVersion string, changes []plugin.ChangelogChange, groups []config.ChangelogGroup) string {
	// Ordered list of (sectionHeading) and the bucket of bullet descriptions.
	var order []string
	buckets := map[string][]string{}
	add := func(section, summary string) {
		if _, ok := buckets[section]; !ok {
			order = append(order, section)
		}
		buckets[section] = append(buckets[section], summary)
	}

	groupSection := func(typ string) (string, bool) {
		for _, g := range groups {
			if g.Type == typ {
				return g.Section, true
			}
		}
		return "", false
	}

	for _, c := range changes {
		switch {
		case c.Breaking:
			add(config.BreakingGroup.Section, c.Summary)
		case c.Type != "":
			if s, ok := groupSection(c.Type); ok {
				add(s, c.Summary)
			} else {
				add(strings.Title(c.Type), c.Summary) //nolint:staticcheck // ASCII type names
			}
		default:
			bump, _ := changeset.ParseBump(c.Bump)
			add(title(bump)+" Changes", c.Summary)
		}
	}

	sortSections(order, groups)

	var b strings.Builder
	fmt.Fprintf(&b, "## %s\n", newVersion)
	for i, section := range order {
		// @changesets (format:false) puts the first section directly under the
		// version header and separates later sections with a blank line.
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "### %s\n\n", section)
		for _, summary := range buckets[section] {
			b.WriteString(formatReleaseLine(summary))
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// sortSections orders sections: Breaking first, then configured group order,
// then the bump sections (Major, Minor, Patch), then anything else alphabetically.
func sortSections(sections []string, groups []config.ChangelogGroup) {
	rank := map[string]int{config.BreakingGroup.Section: 0}
	for i, g := range groups {
		if _, ok := rank[g.Section]; !ok {
			rank[g.Section] = 1 + i
		}
	}
	base := 1 + len(groups)
	for i, s := range []string{"Major Changes", "Minor Changes", "Patch Changes"} {
		rank[s] = base + i
	}
	sort.SliceStable(sections, func(i, j int) bool {
		ri, oki := rank[sections[i]]
		rj, okj := rank[sections[j]]
		if oki && okj {
			return ri < rj
		}
		if oki != okj {
			return oki // ranked sections before unranked
		}
		return sections[i] < sections[j]
	})
}

func title(b changeset.Bump) string {
	switch b {
	case changeset.BumpMajor:
		return "Major"
	case changeset.BumpMinor:
		return "Minor"
	case changeset.BumpPatch:
		return "Patch"
	default:
		return "None"
	}
}

// formatReleaseLine renders a single changelog bullet, mirroring @changesets'
// getReleaseLine: the first line sits on the bullet and continuation lines are
// indented two spaces. Dependency-update descriptions are pre-structured and
// pass through unchanged.
func formatReleaseLine(description string) string {
	if strings.HasPrefix(description, dependencyUpdatesHeader) {
		return "- " + description
	}
	lines := strings.Split(strings.ReplaceAll(description, "\r\n", "\n"), "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t")
	}
	if len(lines) == 1 {
		return "- " + lines[0]
	}
	var b strings.Builder
	b.WriteString("- " + lines[0])
	for _, line := range lines[1:] {
		b.WriteString("\n  " + line)
	}
	return b.String()
}
