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
// It returns whether any version was changed. The dependency cascade is NOT
// recomputed for an override — a manual version stands on its own, matching how
// snapshot/prerelease overrides behave.
func promptVersionOverrides(out io.Writer, plan []*planner.Module) (bool, error) {
	const (
		optAccept = "accept"
		optPatch  = "patch"
		optMinor  = "minor"
		optMajor  = "major"
		optCustom = "custom"
	)

	changed := false
	for _, m := range plan {
		if m.RangeOnly {
			continue // "none" release: no version change to override
		}
		cur := m.Current
		suggested := m.NewVersion()
		patch := cur.RaisePatch()
		minor := cur.RaiseMinor()
		major := cur.RaiseMajor()

		choice := optAccept
		sel := huh.NewSelect[string]().
			Title(fmt.Sprintf("%s — next version (current %s)", m.DisplayName, cur)).
			Description(fmt.Sprintf("computed bump: %s", m.HighestBump())).
			Options(
				huh.NewOption(fmt.Sprintf("Accept %s (computed)", suggested), optAccept),
				huh.NewOption("patch → "+patch.String(), optPatch),
				huh.NewOption("minor → "+minor.String(), optMinor),
				huh.NewOption("major → "+major.String(), optMajor),
				huh.NewOption("custom…", optCustom),
			).
			Value(&choice)
		if err := huh.NewForm(huh.NewGroup(sel)).WithTheme(brand.Theme(brand.AccentChange)).Run(); err != nil {
			return changed, err
		}

		var target string
		switch choice {
		case optAccept:
			continue
		case optPatch:
			target = patch.String()
		case optMinor:
			target = minor.String()
		case optMajor:
			target = major.String()
		case optCustom:
			custom := suggested.String()
			input := huh.NewInput().
				Title(fmt.Sprintf("%s — enter an exact version", m.DisplayName)).
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
				return changed, err
			}
			target = strings.TrimSpace(custom)
		}

		// Only record an override when it actually differs from the computed
		// version, so an "accept by another name" (e.g. picking the bump that
		// equals the suggestion) leaves the plan untouched.
		if target != "" && target != suggested.String() {
			m.VersionOverride = target
			fmt.Fprintln(out, DimStyle.Render(fmt.Sprintf("  override %s → %s", m.DisplayName, target)))
			changed = true
		}
	}
	return changed, nil
}
