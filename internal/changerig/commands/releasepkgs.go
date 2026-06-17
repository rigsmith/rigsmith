package commands

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"github.com/rigsmith/rigsmith/core/planner"
)

// ReleasePkg is one package's disposition in a release: its identity and
// version, whether it will get a new version this run, and whether it's private
// (versioned but never published) or ignored (excluded from the release
// entirely). It's the data behind `shiprig packages` and the doctor packages
// section.
type ReleasePkg struct {
	Name    string
	Eco     string
	Current string
	Next    string // next version when it releases; "" otherwise
	Bump    string // bump level when it releases; "" otherwise
	Private bool   // manifest marked private / publish=false — versioned, never published
	Ignored bool   // matches the config `ignore` list — excluded entirely
}

// Releasing reports whether the package gets a new version this run.
func (p ReleasePkg) Releasing() bool { return p.Next != "" }

// ReleasePackages returns the disposition of every discovered package: which
// will release (from the plan), which are private, and which are ignored.
// Ignored packages are still listed (so a caller can re-include them) — the
// Ignored flag, not omission, marks them.
func ReleasePackages(ctx context.Context, ws *Workspace) ([]ReleasePkg, error) {
	// Discover once and build the plan from the discovered packages, rather than
	// calling BuildPlan (which would scan the workspace a second time).
	pkgs, ecoOf, err := ws.Discover(ctx)
	if err != nil {
		return nil, err
	}
	changesets, _, err := ws.LoadChangesets(ctx, pkgs)
	if err != nil {
		return nil, err
	}
	ws.Config.PerPackageStrategy = ws.Config.StrategyByPackage(ecoOf)
	plan, err := assemblePlan(ctx, ws, changesets, pkgs)
	if err != nil {
		return nil, err
	}
	byName := make(map[string]*planner.Module, len(plan))
	for _, m := range plan {
		byName[m.Name] = m
	}
	out := make([]ReleasePkg, 0, len(pkgs))
	for _, p := range pkgs {
		rp := ReleasePkg{
			Name:    p.Name,
			Eco:     ecoOf[p.Name],
			Current: p.Version,
			Private: p.Private,
			Ignored: ws.Config.IsIgnored(p.Name),
		}
		// A RangeOnly module (dependency-range rewrite, no version bump) is not a
		// "release" for display purposes.
		if m, ok := byName[p.Name]; ok && !m.RangeOnly {
			rp.Next = m.ResolvedVersion()
			rp.Bump = m.HighestBump().String()
		}
		out = append(out, rp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// WriteIgnore writes the changeset config `ignore` array (deduped + sorted),
// splicing into the resolved config file while preserving comments. It returns
// the file path and whether the write landed (false when an existing file can't
// be edited in place). Mirrors rig's AddRepoExclude for the changeset config.
func WriteIgnore(globs []string) (path string, ok bool, err error) {
	path, err = configFile()
	if err != nil {
		return "", false, err
	}
	raw, err := json.Marshal(NormalizeIgnore(globs))
	if err != nil {
		return path, false, err
	}
	return path, configWriter.Set(path, []string{"ignore"}, string(raw)), nil
}

// NormalizeIgnore dedupes (dropping blanks) and sorts an ignore list for a
// stable on-disk diff. Exported so the picker can keep its in-memory list in the
// same shape that WriteIgnore persists.
func NormalizeIgnore(globs []string) []string {
	seen := map[string]bool{}
	uniq := []string{}
	for _, g := range globs {
		g = strings.TrimSpace(g)
		if g != "" && !seen[g] {
			seen[g] = true
			uniq = append(uniq, g)
		}
	}
	sort.Strings(uniq)
	return uniq
}
