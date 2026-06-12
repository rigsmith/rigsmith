// Package semver implements the SemVer 2.0.0 model and the node-semver bump
// rules used by changesets. It is a faithful port of net-changesets'
// Shared/Semver.cs — the ecosystem-agnostic version primitive shared across
// every language adapter.
package semver

import (
	"strconv"
	"strings"
)

// Version is a parsed SemVer 2.0.0 value: major.minor.patch[-prerelease][+build].
//
// Unlike the C# original (a mutable class), Version is an immutable value type:
// the Raise* helpers return a new Version rather than mutating in place, which
// is the idiomatic Go shape and avoids accidental aliasing in the planner.
type Version struct {
	Major int
	Minor int
	Patch int
	// Prerelease is the label without the leading '-' (e.g. "next.0"), empty for a stable release.
	Prerelease string
	// Build is the build metadata without the leading '+' (e.g. "20260609"), empty when absent. Ignored for precedence.
	Build string
}

// Empty is the 0.0.0 zero version.
var Empty = Version{}

// New constructs a Version.
func New(major, minor, patch int, prerelease, build string) Version {
	return Version{Major: major, Minor: minor, Patch: patch, Prerelease: prerelease, Build: build}
}

// PrereleaseNumber returns the numeric second pre-release identifier (e.g. 3 for
// "next.3"), or -1 when absent or non-numeric.
func (v Version) PrereleaseNumber() int {
	if v.Prerelease == "" {
		return -1
	}
	ids := strings.Split(v.Prerelease, ".")
	if len(ids) >= 2 {
		if n, err := strconv.Atoi(ids[1]); err == nil && !hasSign(ids[1]) {
			return n
		}
	}
	return -1
}

// RaiseMajor bumps to the next stable major, dropping pre-release and build
// metadata. Follows the node-semver rule: a pre-release whose minor and patch
// are both zero graduates without incrementing (1.0.0-next.0 -> 1.0.0).
func (v Version) RaiseMajor() Version {
	major := v.Major
	if v.Minor != 0 || v.Patch != 0 || v.Prerelease == "" {
		major++
	}
	return Version{Major: major}
}

// RaiseMinor bumps to the next stable minor, dropping pre-release and build
// metadata. A pre-release at patch zero graduates without incrementing
// (1.1.0-next.0 -> 1.1.0).
func (v Version) RaiseMinor() Version {
	minor := v.Minor
	if v.Patch != 0 || v.Prerelease == "" {
		minor++
	}
	return Version{Major: v.Major, Minor: minor}
}

// RaisePatch bumps to the next stable patch, dropping pre-release and build
// metadata. A pre-release graduates to its own patch without incrementing
// (1.1.0-next.0 -> 1.1.0).
func (v Version) RaisePatch() Version {
	patch := v.Patch
	if v.Prerelease == "" {
		patch++
	}
	return Version{Major: v.Major, Minor: v.Minor, Patch: patch}
}

// WithPrerelease returns a copy carrying the given pre-release label (and
// optional build metadata), leaving the core version unchanged.
func (v Version) WithPrerelease(prerelease, build string) Version {
	if build == "" {
		build = v.Build
	}
	return Version{Major: v.Major, Minor: v.Minor, Patch: v.Patch, Prerelease: prerelease, Build: build}
}

// String renders the canonical SemVer string.
func (v Version) String() string {
	var b strings.Builder
	b.WriteString(strconv.Itoa(v.Major))
	b.WriteByte('.')
	b.WriteString(strconv.Itoa(v.Minor))
	b.WriteByte('.')
	b.WriteString(strconv.Itoa(v.Patch))
	if v.Prerelease != "" {
		b.WriteByte('-')
		b.WriteString(v.Prerelease)
	}
	if v.Build != "" {
		b.WriteByte('+')
		b.WriteString(v.Build)
	}
	return b.String()
}

// Parse parses a version string. ok is false if the string is not valid SemVer.
func Parse(s string) (v Version, ok bool) {
	if strings.TrimSpace(s) == "" {
		return Version{}, false
	}
	core := s

	var build string
	if i := strings.IndexByte(core, '+'); i >= 0 {
		build = core[i+1:]
		core = core[:i]
		if build == "" || !isValidDotSeparated(build, true) {
			return Version{}, false
		}
	}

	var prerelease string
	if i := strings.IndexByte(core, '-'); i >= 0 {
		prerelease = core[i+1:]
		core = core[:i]
		if prerelease == "" || !isValidDotSeparated(prerelease, false) {
			return Version{}, false
		}
	}

	major, minor, patch, ok := parseCore(core)
	if !ok {
		return Version{}, false
	}
	return Version{Major: major, Minor: minor, Patch: patch, Prerelease: prerelease, Build: build}, true
}

// MustParse parses s or panics. For tests and constant version strings.
func MustParse(s string) Version {
	v, ok := Parse(s)
	if !ok {
		panic("semver: invalid version " + s)
	}
	return v
}

// parseCore parses "major.minor.patch", "major.minor", or "major". Missing
// trailing components default to 0, matching System.Version semantics used by
// the C# original.
func parseCore(core string) (major, minor, patch int, ok bool) {
	parts := strings.Split(core, ".")
	if len(parts) < 1 || len(parts) > 4 {
		return 0, 0, 0, false
	}
	nums := make([]int, len(parts))
	for i, p := range parts {
		if p == "" || hasSign(p) {
			return 0, 0, 0, false
		}
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return 0, 0, 0, false
		}
		nums[i] = n
	}
	major = nums[0]
	if len(nums) > 1 {
		minor = nums[1]
	}
	if len(nums) > 2 {
		patch = nums[2]
	}
	return major, minor, patch, true
}

// Compare returns -1, 0, or +1 by SemVer 2.0.0 precedence: major, minor, patch,
// then pre-release. A pre-release has lower precedence than the otherwise-equal
// stable version. Build metadata is ignored.
func Compare(a, b Version) int {
	if c := cmpInt(a.Major, b.Major); c != 0 {
		return c
	}
	if c := cmpInt(a.Minor, b.Minor); c != 0 {
		return c
	}
	if c := cmpInt(a.Patch, b.Patch); c != 0 {
		return c
	}
	switch {
	case a.Prerelease == "" && b.Prerelease == "":
		return 0
	case a.Prerelease == "":
		return 1
	case b.Prerelease == "":
		return -1
	default:
		return comparePrerelease(a.Prerelease, b.Prerelease)
	}
}

func comparePrerelease(left, right string) int {
	l := strings.Split(left, ".")
	r := strings.Split(right, ".")
	shared := min(len(l), len(r))
	for i := 0; i < shared; i++ {
		if c := comparePrereleaseIdentifier(l[i], r[i]); c != 0 {
			return c
		}
	}
	return cmpInt(len(l), len(r))
}

func comparePrereleaseIdentifier(left, right string) int {
	lv, lNumeric := parseNumericID(left)
	rv, rNumeric := parseNumericID(right)
	switch {
	case lNumeric && rNumeric:
		return cmpInt64(lv, rv)
	case lNumeric:
		return -1 // numeric identifiers sort below alphanumeric
	case rNumeric:
		return 1
	default:
		return strings.Compare(left, right)
	}
}

func parseNumericID(s string) (int64, bool) {
	if hasSign(s) {
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

func isValidDotSeparated(value string, allowLeadingZeroNumeric bool) bool {
	for _, id := range strings.Split(value, ".") {
		if id == "" {
			return false
		}
		if allDigits(id) {
			if !allowLeadingZeroNumeric && len(id) > 1 && id[0] == '0' {
				return false
			}
			continue
		}
		for i := 0; i < len(id); i++ {
			c := id[i]
			if !(c >= '0' && c <= '9' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c == '-') {
				return false
			}
		}
	}
	return true
}

func allDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return len(s) > 0
}

func hasSign(s string) bool {
	return len(s) > 0 && (s[0] == '+' || s[0] == '-')
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func cmpInt64(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}
