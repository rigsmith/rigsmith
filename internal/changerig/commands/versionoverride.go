package commands

import (
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/rigsmith/rigsmith/core/brand"
	"github.com/rigsmith/rigsmith/core/planner"
	"github.com/rigsmith/rigsmith/core/semver"
)

// promptVersionOverrides offers a release-it–style override for each releasing
// package: the computed next version is the default, and the user can accept it,
// pick a different bump level, or type an exact version. Overrides are written
// as a literal VersionOverride (the same field snapshot/prerelease use), so the
// manifest write and the changelog header both pick them up.
//
// Modules that write the same version (a shared version file / lockstep group)
// are prompted once and overridden together, so the group can't end up with
// conflicting targets. It returns whether any version was changed. The
// dependency cascade is NOT recomputed for an override — a manual version stands
// on its own, matching how snapshot/prerelease overrides behave.
func promptVersionOverrides(out io.Writer, plan []*planner.Module) (bool, error) {
	changed := false
	for _, group := range groupByVersionFile(plan) {
		rep := group[0]
		target, err := promptVersionTarget(rep, group)
		if err != nil {
			return changed, err
		}
		if target == "" || target == rep.NewVersion().String() {
			continue // accepted the computed version
		}
		// Apply coherently to every module that writes the same version, so a
		// shared version file isn't left inconsistent (last write wins).
		for _, m := range group {
			m.VersionOverride = target
		}
		fmt.Fprintln(out, DimStyle.Render(fmt.Sprintf("  override %s → %s", groupLabel(group), target)))
		changed = true
	}
	return changed, nil
}

// groupByVersionFile groups releasing modules by where their version is written:
// a shared version file when set, else the module's own manifest (unique, so
// inline/independent modules each form a singleton group). Modules sharing a
// version file move in lockstep and must be overridden together. First-seen
// order is preserved.
func groupByVersionFile(plan []*planner.Module) [][]*planner.Module {
	var groups [][]*planner.Module
	index := map[string]int{}
	for _, m := range plan {
		if m.RangeOnly {
			continue // "none" release: no version change to override
		}
		key := m.EffectiveVersionFile()
		if i, ok := index[key]; ok {
			groups[i] = append(groups[i], m)
			continue
		}
		index[key] = len(groups)
		groups = append(groups, []*planner.Module{m})
	}
	return groups
}

// groupLabel names a version-file group for prompts and the status line: the
// representative package, plus a "+N sharing this version" when several packages
// move together.
func groupLabel(group []*planner.Module) string {
	label := group[0].DisplayName
	if n := len(group) - 1; n > 0 {
		label += fmt.Sprintf(" (+%d sharing this version)", n)
	}
	return label
}

// promptVersionTarget shows the chooser for one version-file group and returns
// the chosen version string, or "" to accept the computed one.
func promptVersionTarget(rep *planner.Module, group []*planner.Module) (string, error) {
	const (
		optAccept = "accept"
		optPatch  = "patch"
		optMinor  = "minor"
		optMajor  = "major"
		optCustom = "custom"
	)
	cur := rep.Current
	suggested := rep.NewVersion()
	patch := cur.RaisePatch()
	minor := cur.RaiseMinor()
	major := cur.RaiseMajor()

	choice := optAccept
	sel := huh.NewSelect[string]().
		Title(fmt.Sprintf("%s — next version (current %s)", groupLabel(group), cur)).
		Description(fmt.Sprintf("computed bump: %s", rep.HighestBump())).
		Options(
			huh.NewOption(fmt.Sprintf("Accept %s (computed)", suggested), optAccept),
			huh.NewOption("patch → "+patch.String(), optPatch),
			huh.NewOption("minor → "+minor.String(), optMinor),
			huh.NewOption("major → "+major.String(), optMajor),
			huh.NewOption("custom…", optCustom),
		).
		Value(&choice)
	if err := huh.NewForm(huh.NewGroup(sel)).WithTheme(brand.Theme(brand.AccentChange)).Run(); err != nil {
		return "", err
	}

	switch choice {
	case optPatch:
		return patch.String(), nil
	case optMinor:
		return minor.String(), nil
	case optMajor:
		return major.String(), nil
	case optCustom:
		custom := suggested.String()
		input := huh.NewInput().
			Title(fmt.Sprintf("%s — enter an exact version", groupLabel(group))).
			Value(&custom).
			Validate(func(s string) error {
				v, ok := semver.Parse(strings.TrimSpace(s))
				if !ok {
					return fmt.Errorf("not a valid semver version")
				}
				if semver.Compare(v, cur) <= 0 {
					return fmt.Errorf("must be greater than current %s", cur)
				}
				return nil
			})
		if err := huh.NewForm(huh.NewGroup(input)).WithTheme(brand.Theme(brand.AccentChange)).Run(); err != nil {
			return "", err
		}
		// Store the canonical form: semver.Parse accepts "1.2" and leading zeros,
		// so re-emitting via String() keeps manifests and changelog headers from
		// carrying a non-canonical version string.
		v, _ := semver.Parse(strings.TrimSpace(custom))
		return v.String(), nil
	default: // optAccept
		return "", nil
	}
}

// applyReleaseAs is the prompt's "custom version" without the prompt, for CI:
// each spec is `<package>=<version>`, or a bare `<version>` when the run
// releases a single version. The version must be valid semver above the
// package's current one, and like a prompted override it applies to every
// package sharing that version file and doesn't recompute the cascade. A
// package that isn't releasing has nothing to override: an error, so a typo
// or a missing changeset isn't silently a no-op.
func applyReleaseAs(out io.Writer, plan []*planner.Module, specs []string) error {
	groups := groupByVersionFile(plan)
	groupOf := map[string][]*planner.Module{}
	for _, group := range groups {
		for _, m := range group {
			groupOf[m.Name] = group
		}
	}
	for _, spec := range specs {
		name, version, named := strings.Cut(spec, "=")
		var group []*planner.Module
		switch {
		case named:
			group = groupOf[strings.TrimSpace(name)]
			if group == nil {
				return fmt.Errorf("--release-as %s: %s isn't releasing in this run; a changeset (or commit) has to release it first", spec, name)
			}
		case len(groups) == 1:
			version, group = name, groups[0]
		default:
			return fmt.Errorf("--release-as %s: %d versions are releasing, so name the package (<package>=<version>)", spec, len(groups))
		}
		rep := group[0]
		v, ok := semver.Parse(strings.TrimSpace(version))
		if !ok {
			return fmt.Errorf("--release-as %s: %q is not a valid semver version", spec, version)
		}
		if semver.Compare(v, rep.Current) <= 0 {
			return fmt.Errorf("--release-as %s: must be greater than the current %s", spec, rep.Current)
		}
		for _, m := range group {
			m.VersionOverride = v.String()
		}
		fmt.Fprintln(out, DimStyle.Render(fmt.Sprintf("  release as %s → %s", groupLabel(group), v)))
	}
	return nil
}
