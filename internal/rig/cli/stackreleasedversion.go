package cli

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Keep all four NuGet numeric components. The general SemVer helper discards
// revisions and therefore cannot establish package-delivery or downgrade safety.
type stackNugetVersion struct {
	numbers [4]uint64
	release string
}

func stackParseNugetVersion(raw string) (stackNugetVersion, bool) {
	var v stackNugetVersion
	s := strings.TrimSpace(raw)
	validIDs := func(raw string, prerelease bool) bool {
		for _, id := range strings.Split(raw, ".") {
			if id == "" {
				return false
			}
			numeric := true
			for _, c := range id {
				if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '-') {
					return false
				}
				if c < '0' || c > '9' {
					numeric = false
				}
			}
			// Refuse ambiguous legacy labels rather than silently normalizing them.
			if prerelease && numeric {
				if len(id) > 1 && id[0] == '0' {
					return false
				}
				// NuGet's comparer uses Int32.TryParse for numeric labels.
				// Reject overflow rather than inventing a different precedence.
				if _, err := strconv.ParseUint(id, 10, 31); err != nil {
					return false
				}
			}
		}
		return true
	}
	if core, metadata, ok := strings.Cut(s, "+"); ok {
		if !validIDs(metadata, false) {
			return v, false
		}
		s = core
	}
	if core, release, ok := strings.Cut(s, "-"); ok {
		if !validIDs(release, true) {
			return v, false
		}
		v.release = strings.ToLower(release)
		s = core
	}
	parts := strings.Split(s, ".")
	if len(parts) < 2 || len(parts) > 4 {
		return v, false
	}
	for i, part := range parts {
		if part == "" {
			return v, false
		}
		for _, c := range part {
			if c < '0' || c > '9' {
				return v, false
			}
		}
		n, err := strconv.ParseUint(part, 10, 31)
		if err != nil {
			return v, false
		}
		v.numbers[i] = n
	}
	return v, true
}

func (v stackNugetVersion) normalized() string {
	s := fmt.Sprintf("%d.%d.%d", v.numbers[0], v.numbers[1], v.numbers[2])
	if v.numbers[3] != 0 {
		s += fmt.Sprintf(".%d", v.numbers[3])
	}
	if v.release != "" {
		s += "-" + v.release
	}
	return s
}

func stackCompareNuget(a, b stackNugetVersion) int {
	for i, n := range a.numbers {
		if c := cmp.Compare(n, b.numbers[i]); c != 0 {
			return c
		}
	}
	if a.release == "" && b.release == "" {
		return 0
	}
	if a.release == "" {
		return 1
	}
	if b.release == "" {
		return -1
	}
	left, right := strings.Split(a.release, "."), strings.Split(b.release, ".")
	numeric := func(s string) bool {
		for _, c := range s {
			if c < '0' || c > '9' {
				return false
			}
		}
		return true
	}
	for i := 0; i < min(len(left), len(right)); i++ {
		l, r := left[i], right[i]
		ln, rn := numeric(l), numeric(r)
		if ln && rn {
			if c := cmp.Compare(len(l), len(r)); c != 0 {
				return c
			}
		} else if ln != rn {
			if ln {
				return -1
			}
			return 1
		}
		if c := strings.Compare(l, r); c != 0 {
			return c
		}
	}
	return cmp.Compare(len(left), len(right))
}

// Do not trust feed ordering, or sort revision components lexicographically.
func stackReleaseCandidates(raw []string) ([]string, error) {
	var versions []stackNugetVersion
	for _, s := range raw {
		v, ok := stackParseNugetVersion(s)
		if !ok {
			return nil, fmt.Errorf("can't tell: unsupported feed version")
		}
		if v.release == "" {
			versions = append(versions, v)
		}
	}
	slices.SortFunc(versions, func(a, b stackNugetVersion) int { return stackCompareNuget(b, a) })
	var out []string
	for _, v := range versions {
		normalized := v.normalized()
		if len(out) == 0 || out[len(out)-1] != normalized {
			out = append(out, normalized)
		}
		if len(out) == 20 {
			break
		}
	}
	return out, nil
}
