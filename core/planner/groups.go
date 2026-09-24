package planner

import (
	"sort"

	"github.com/rigsmith/rigsmith/core/changeset"
	"github.com/rigsmith/rigsmith/core/config"
)

// ReleaseGroups partitions a plan into the packages that have to be versioned
// together, so a caller can release each group on its own schedule (a version
// PR per group, say) with `version --only`. Two packages share a group when:
//
//   - one changeset names both: versioning one without the other is a mixed
//     changeset, which is refused;
//   - one depends on the other in this plan (a cascade, or a range-only
//     rewrite): skipping the dependency while releasing the dependent is
//     refused, and the dependent's new range names the dependency's version;
//   - they're in the same fixed or linked group, which coordinates their
//     versions;
//   - they share a version file, which moves them together.
//
// It returns each planned package's group, named by the group's
// alphabetically first member, so the name is stable across runs while the
// membership is.
func ReleaseGroups(plan []*Module, changesets []*changeset.Changeset, cfg *config.Config) map[string]string {
	parent := map[string]string{}
	for _, m := range plan {
		parent[m.Name] = m.Name
	}
	var find func(string) string
	find = func(n string) string {
		if parent[n] != n {
			parent[n] = find(parent[n])
		}
		return parent[n]
	}
	union := func(a, b string) {
		if _, ok := parent[a]; !ok {
			return
		}
		if _, ok := parent[b]; !ok {
			return
		}
		ra, rb := find(a), find(b)
		if ra == rb {
			return
		}
		// The smaller name roots the group, so the group's name is its first
		// member.
		if rb < ra {
			ra, rb = rb, ra
		}
		parent[rb] = ra
	}
	// Only planned names join: a fixed group or changeset whose first name
	// isn't releasing still joins the rest.
	unionAll := func(names []string) {
		anchor := ""
		for _, n := range names {
			if _, ok := parent[n]; !ok {
				continue
			}
			if anchor == "" {
				anchor = n
				continue
			}
			union(anchor, n)
		}
	}

	for _, cs := range changesets {
		names := make([]string, 0, len(cs.Releases))
		for _, r := range cs.Releases {
			names = append(names, r.Name)
		}
		unionAll(names)
	}
	versionFiles := map[string][]string{}
	for _, m := range plan {
		for _, l := range m.depLinks {
			union(m.Name, l.dep.Name)
		}
		if m.VersionFile != "" {
			versionFiles[m.VersionFile] = append(versionFiles[m.VersionFile], m.Name)
		}
	}
	for _, grp := range cfg.Fixed {
		unionAll(grp)
	}
	for _, grp := range cfg.Linked {
		unionAll(grp)
	}
	files := make([]string, 0, len(versionFiles))
	for f := range versionFiles {
		files = append(files, f)
	}
	sort.Strings(files)
	for _, f := range files {
		unionAll(versionFiles[f])
	}

	groups := make(map[string]string, len(parent))
	for name := range parent {
		groups[name] = find(name)
	}
	return groups
}
