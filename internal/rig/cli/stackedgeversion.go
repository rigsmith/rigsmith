package cli

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

type stackEdgeVersion struct {
	Version     string
	Publishable bool // number 0 is reserved for local packs
}

func stackEdgeValidateLabel(label string) error {
	if label == "" {
		return nil // default: edge
	}
	if strings.TrimSpace(label) != label || strings.Contains(label, "+") {
		return fmt.Errorf("edge label must contain valid NuGet prerelease identifiers")
	}
	if _, ok := stackParseNugetVersion("1.0.0-" + label + ".0"); !ok {
		return fmt.Errorf("edge label must contain valid NuGet prerelease identifiers")
	}
	return nil
}

// stackEdgeComputeVersion implements the R/T rule without feed or tree access.
// A winning prerelease T gets dot-separated suffix identifiers so an edge of
// rc.1 still sorts below rc.2. Build metadata does not affect precedence.
func stackEdgeComputeVersion(released, tree, label string, number uint64) (stackEdgeVersion, error) {
	var result stackEdgeVersion
	if number > math.MaxInt32 {
		return result, fmt.Errorf("edge number must fit a 32-bit NuGet prerelease identifier")
	}
	if err := stackEdgeValidateLabel(label); err != nil {
		return result, err
	}
	if label == "" {
		label = "edge"
	}
	var r, t stackNugetVersion
	if released != "" {
		var ok bool
		r, ok = stackParseNugetVersion(released)
		if !ok || r.release != "" {
			return result, fmt.Errorf("edge public version R must be a stable NuGet version")
		}
	}
	if tree != "" {
		var ok bool
		t, ok = stackParseNugetVersion(tree)
		if !ok {
			return result, fmt.Errorf("edge in-tree version T must be a NuGet version or empty")
		}
	}
	if released == "" && tree == "" {
		return result, fmt.Errorf("edge needs a public version R or an in-tree version T")
	}
	base := t
	if released != "" && (tree == "" || stackCompareNuget(t, r) <= 0) {
		base = r
		if base.numbers[2] == math.MaxInt32 {
			return result, fmt.Errorf("edge patch increment would overflow a 32-bit NuGet version component")
		}
		base.numbers[2]++
		// A patch bump starts a new patch even for a four-component NuGet R.
		base.numbers[3] = 0
	}
	separator := "-"
	if base.release != "" {
		separator = "."
	}
	result.Version = base.normalized() + separator + strings.ToLower(label) + "." + strconv.FormatUint(number, 10)
	if _, ok := stackParseNugetVersion(result.Version); !ok {
		return stackEdgeVersion{}, fmt.Errorf("computed edge version is not a supported NuGet version")
	}
	result.Publishable = number != 0
	return result, nil
}

// stackEdgeCheckMonotonic compares one base/label series. Newest may be empty
// when no version in this series exists. A different series is inconclusive,
// not permission to publish; callers must query the matching base and label.
func stackEdgeCheckMonotonic(version, newest string) error {
	parse := func(raw string) (stackNugetVersion, string, error) {
		v, ok := stackParseNugetVersion(raw)
		i := strings.LastIndex(v.release, ".")
		if !ok || i <= 0 {
			return v, "", fmt.Errorf("edge monotonicity needs a version ending in <label>.<number>")
		}
		prefix, n := v.release[:i], v.release[i+1:]
		if _, err := strconv.ParseUint(n, 10, 31); err != nil {
			return v, "", fmt.Errorf("edge monotonicity needs a 32-bit numeric run number")
		}
		return v, prefix, nil
	}
	candidate, series, err := parse(version)
	if err != nil {
		return err
	}
	if newest == "" {
		return nil
	}
	previous, oldSeries, err := parse(newest)
	if err != nil {
		return err
	}
	if candidate.numbers != previous.numbers || series != oldSeries {
		return fmt.Errorf("newest edge version must belong to the same base and label")
	}
	if stackCompareNuget(candidate, previous) <= 0 {
		return fmt.Errorf("edge number must increase beyond the feed's newest version %s", previous.normalized())
	}
	return nil
}
