package planner

import (
	"fmt"
	"strings"
	"time"

	"github.com/rigsmith/core/changeset"
	"github.com/rigsmith/core/plugin"
	"github.com/rigsmith/core/semver"
)

// Mode selects how versions are computed for a release run.
type Mode int

const (
	// ModeNormal is the standard stable bump.
	ModeNormal Mode = iota
	// ModePre is a prerelease run (-{tag}.{n} suffix), changesets kept.
	ModePre
	// ModeSnapshot is a throwaway snapshot run (0.0.0-{suffix}), changesets kept.
	ModeSnapshot
	// ModeExit graduates prerelease packages to their stable version.
	ModeExit
)

// PreVersion is the prerelease version for a module: the next stable bump with a
// -{tag}.{n} suffix, where n is one past the module's current prerelease number
// (1.0.0 + minor + tag "next" → 1.1.0-next.0; a later run → 1.1.0-next.1).
// Port of @changesets incrementVersion / net-changesets ReleaseVersionPlanner.
func PreVersion(m *Module, tag string) string {
	stableBase := m.NewVersion()
	preNumber := m.Current.PrereleaseNumber() + 1
	return fmt.Sprintf("%s-%s.%d", stableBase, tag, preNumber)
}

// SnapshotVersion is {base}-{suffix}: base is 0.0.0 by default, or the calculated
// next stable version when useCalculatedVersion is set.
func SnapshotVersion(m *Module, useCalculatedVersion bool, suffix string) string {
	base := "0.0.0"
	if useCalculatedVersion {
		base = m.NewVersion().String()
	}
	return base + "-" + suffix
}

// ApplyPre stamps prerelease version overrides onto every module in the plan,
// then re-materializes dependency lines/rewrites so they carry the prerelease
// versions with the range operator preserved (^1.0.0 → ^2.0.0-next.0, per Node).
func ApplyPre(plan []*Module, tag string) {
	for _, m := range plan {
		if m.RangeOnly {
			continue // a "none" release keeps its version in every mode
		}
		m.VersionOverride = PreVersion(m, tag)
	}
	for _, m := range plan {
		m.materializeDeps(false)
	}
}

// ApplySnapshot stamps snapshot version overrides onto every module in the plan,
// then re-materializes dependency lines/rewrites pinned to the exact snapshot
// version (^1.0.0 → 0.0.0-canary-…, operator dropped, per Node).
func ApplySnapshot(plan []*Module, useCalculatedVersion bool, suffix string) {
	for _, m := range plan {
		if m.RangeOnly {
			continue // a "none" release keeps its version in every mode
		}
		m.VersionOverride = SnapshotVersion(m, useCalculatedVersion, suffix)
	}
	for _, m := range plan {
		m.materializeDeps(true)
	}
}

// GraduatePrereleases extends the plan (on `pre exit`) with a stable-graduating
// entry for every package still on a prerelease version that has no changeset.
// A patch bump on a prerelease stabilizes it (1.1.0-next.3 → 1.1.0).
func GraduatePrereleases(plan []*Module, packages []plugin.Package) []*Module {
	covered := map[string]bool{}
	for _, m := range plan {
		covered[m.Name] = true
	}
	for _, p := range packages {
		if covered[p.Name] {
			continue
		}
		v, ok := semver.Parse(p.Version)
		if !ok || v.Prerelease == "" {
			continue
		}
		m := newModule(p)
		m.cascadeBump = changeset.BumpPatch // patch on a prerelease stabilizes it
		plan = append(plan, m)
	}
	return plan
}

// SnapshotSuffix composes the suffix of a snapshot version (the part after the
// base and '-'), mirroring @changesets getSnapshotSuffix. With no template the
// suffix is {tag}-{datetime} (or just {datetime} when tag is empty); a template
// may use the {commit}, {tag}, {datetime}, and {timestamp} placeholders.
func SnapshotSuffix(template, tag, commit string, ref time.Time) (string, error) {
	utc := ref.UTC()
	timestamp := fmt.Sprintf("%d", utc.UnixMilli())
	datetime := utc.Format("20060102150405")

	if template == "" {
		parts := []string{}
		if tag != "" {
			parts = append(parts, tag)
		}
		parts = append(parts, datetime)
		return strings.Join(parts, "-"), nil
	}

	if !strings.Contains(template, "{tag}") && tag != "" {
		return "", fmt.Errorf("snapshot template missing {tag} placeholder but a tag is set (%q)", tag)
	}

	suffix := template
	for _, ph := range []struct{ key, val string }{
		{"commit", commit}, {"tag", tag}, {"timestamp", timestamp}, {"datetime", datetime},
	} {
		token := "{" + ph.key + "}"
		if !strings.Contains(suffix, token) {
			continue
		}
		if ph.val == "" && ph.key != "tag" {
			return "", fmt.Errorf("snapshot template uses %s but no value is defined", token)
		}
		suffix = strings.ReplaceAll(suffix, token, ph.val)
	}
	return suffix, nil
}
