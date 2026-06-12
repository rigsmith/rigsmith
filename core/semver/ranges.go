package semver

import (
	"strconv"
	"strings"
)

// Satisfies reports whether version v falls within the npm-style range string.
//
// It is used by the changeset release planner to decide whether a dependent
// package needs a bump: when a dependency's NEW version no longer satisfies the
// range the dependent declared, that triggers a cascade. The goal is therefore
// to match node-semver's behaviour for the COMMON monorepo range shapes, not to
// be a bit-for-bit reimplementation of the full grammar.
//
// Supported syntax:
//
//   - "" / "*" / "x" / "X"            -> matches anything
//   - "workspace:" prefix (pnpm/yarn) -> stripped, then the remainder is parsed
//     as a normal range; "workspace:*" and a bare "workspace:" match anything
//   - "||"                            -> OR of sub-ranges (any match wins)
//   - whitespace within a sub-range   -> AND of comparators (all must hold)
//   - "^1.2.3"                        -> compatible-with, with leading-zero rules
//   - "~1.2.3" / "~1.2" / "~1"        -> approximately-equivalent
//   - ">=" ">" "<=" "<" "=" / bare    -> comparator on the core version
//   - X-ranges / partials: "1", "1.2", "1.x", "1.2.x", "1.*"
//
// Prerelease handling is intentionally simplified. node-semver only lets a
// prerelease version satisfy a comparator when that comparator names the same
// [major, minor, patch] tuple; we do not implement that gate. Instead we lean on
// the existing Compare, which already orders a prerelease below its own stable
// release. For the planner's purposes (is the resolved version inside the
// declared range?) this approximation is sufficient: resolved versions are
// almost always stable, and the few prerelease edge cases node-semver would
// reject are not load-bearing for cascade decisions.
func Satisfies(v Version, rangeStr string) bool {
	rangeStr = strings.TrimSpace(rangeStr)
	rangeStr = stripWorkspacePrefix(rangeStr)
	if isWildcardRange(rangeStr) {
		return true
	}
	// OR: any sub-range matching is enough.
	for _, sub := range strings.Split(rangeStr, "||") {
		if satisfiesSubRange(v, sub) {
			return true
		}
	}
	return false
}

// SatisfiesString parses versionStr and reports whether it satisfies rangeStr.
// It returns false when versionStr is not a valid version.
func SatisfiesString(versionStr, rangeStr string) bool {
	v, ok := Parse(versionStr)
	if !ok {
		return false
	}
	return Satisfies(v, rangeStr)
}

// stripWorkspacePrefix removes a leading "workspace:" protocol marker. pnpm and
// yarn use it to mean "the locally linked package"; for satisfaction purposes
// the remainder is a normal range (and the linked version always satisfies, so
// an empty/"*" remainder matches anything).
func stripWorkspacePrefix(s string) string {
	if rest, ok := strings.CutPrefix(s, "workspace:"); ok {
		return strings.TrimSpace(rest)
	}
	return s
}

// isWildcardRange reports whether s is one of the catch-all ranges.
func isWildcardRange(s string) bool {
	return s == "" || s == "*" || s == "x" || s == "X"
}

// satisfiesSubRange evaluates a single AND-joined sub-range: every
// whitespace-separated comparator must hold.
func satisfiesSubRange(v Version, sub string) bool {
	fields := strings.Fields(sub)
	if len(fields) == 0 {
		// An empty sub-range (e.g. a bare "" between "||" tokens) matches all.
		return true
	}
	for _, c := range fields {
		if !satisfiesComparator(v, c) {
			return false
		}
	}
	return true
}

// satisfiesComparator evaluates one comparator token against v.
func satisfiesComparator(v Version, c string) bool {
	c = strings.TrimSpace(c)
	if isWildcardRange(c) {
		return true
	}

	switch {
	case strings.HasPrefix(c, "^"):
		lo, hi, ok := caretBounds(c[1:])
		if !ok {
			return false
		}
		return Compare(v, lo) >= 0 && Compare(v, hi) < 0
	case strings.HasPrefix(c, "~"):
		lo, hi, ok := tildeBounds(c[1:])
		if !ok {
			return false
		}
		return Compare(v, lo) >= 0 && Compare(v, hi) < 0
	case strings.HasPrefix(c, ">="):
		w, ok := parsePartialMin(c[2:])
		return ok && Compare(v, w) >= 0
	case strings.HasPrefix(c, "<="):
		// "<=" on a partial means "below the top of that partial's window",
		// e.g. "<=1.2" allows up to and including 1.2.x. We model it as the
		// upper bound of the partial's X-range window (exclusive of the next
		// step), falling back to an exact compare for a full version.
		lo, hi, full, ok := partialWindow(c[2:])
		if !ok {
			return false
		}
		if full {
			return Compare(v, lo) <= 0
		}
		return Compare(v, hi) < 0
	case strings.HasPrefix(c, ">"):
		// ">1.2" means strictly above the whole 1.2.x window.
		lo, hi, full, ok := partialWindow(c[1:])
		if !ok {
			return false
		}
		if full {
			return Compare(v, lo) > 0
		}
		return Compare(v, hi) >= 0
	case strings.HasPrefix(c, "<"):
		w, ok := parsePartialMin(c[1:])
		return ok && Compare(v, w) < 0
	case strings.HasPrefix(c, "="):
		return satisfiesBare(v, c[1:])
	default:
		return satisfiesBare(v, c)
	}
}

// satisfiesBare handles a bare token: an exact full version is "==", while a
// partial / X-range ("1", "1.2", "1.x") expands to its window.
func satisfiesBare(v Version, s string) bool {
	lo, hi, full, ok := partialWindow(s)
	if !ok {
		return false
	}
	if full {
		return Compare(v, lo) == 0
	}
	return Compare(v, lo) >= 0 && Compare(v, hi) < 0
}

// partial holds the components actually present in a (possibly partial) version
// token, plus where the first X / missing component began.
type partial struct {
	major, minor, patch int
	// specified is the count of leading components pinned to a concrete number
	// (0, 1, 2, or 3). Components beyond that were "x"/"*" or simply absent.
	specified int
	pre       string
	build     string
}

// parsePartial parses tokens like "1", "1.2", "1.2.3", "1.x", "1.*", "1.2.x".
// Trailing "x"/"X"/"*" or missing components reduce `specified`. A concrete
// component appearing AFTER an x (e.g. "1.x.3") is rejected.
func parsePartial(s string) (partial, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return partial{specified: 0}, true
	}

	// Peel off build / prerelease so the numeric core can be split on '.'.
	core := s
	var build string
	if i := strings.IndexByte(core, '+'); i >= 0 {
		build = core[i+1:]
		core = core[:i]
	}
	var pre string
	if i := strings.IndexByte(core, '-'); i >= 0 {
		pre = core[i+1:]
		core = core[:i]
	}

	parts := strings.Split(core, ".")
	if len(parts) > 3 {
		return partial{}, false
	}

	var p partial
	p.pre = pre
	p.build = build
	nums := []int{0, 0, 0}
	specified := 0
	sawX := false
	for i, raw := range parts {
		if raw == "" {
			return partial{}, false
		}
		if raw == "x" || raw == "X" || raw == "*" {
			sawX = true
			continue
		}
		if sawX {
			// Concrete component after an x-component is not a shape we model.
			return partial{}, false
		}
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 || hasSign(raw) {
			return partial{}, false
		}
		nums[i] = n
		specified = i + 1
	}
	p.major, p.minor, p.patch = nums[0], nums[1], nums[2]
	p.specified = specified
	return p, true
}

// partialWindow returns the inclusive lower bound `lo` and exclusive upper bound
// `hi` of the X-range window a partial denotes, plus `full` = true when all
// three components were specified (in which case `lo` is the exact version and
// `hi` is meaningless).
//
//	"1"     -> [1.0.0, 2.0.0)
//	"1.2"   -> [1.2.0, 1.3.0)
//	"1.2.3" -> exact 1.2.3 (full)
func partialWindow(s string) (lo, hi Version, full bool, ok bool) {
	p, parsed := parsePartial(s)
	if !parsed {
		return Version{}, Version{}, false, false
	}
	lo = Version{Major: p.major, Minor: p.minor, Patch: p.patch, Prerelease: p.pre, Build: p.build}
	switch p.specified {
	case 0:
		// "*" / "x" already handled by callers, but be permissive: whole range.
		return Version{}, Version{Major: 1 << 30}, false, true
	case 1:
		return lo, Version{Major: p.major + 1}, false, true
	case 2:
		return lo, Version{Major: p.major, Minor: p.minor + 1}, false, true
	default:
		return lo, Version{}, true, true
	}
}

// parsePartialMin returns the lower bound of a partial, used by ">=" and "<"
// where node-semver substitutes 0 for the missing/x components.
func parsePartialMin(s string) (Version, bool) {
	lo, _, _, ok := partialWindow(s)
	return lo, ok
}

// caretBounds returns the [lo, hi) bounds for a caret range, honouring the
// leading-zero rules:
//
//	^1.2.3 -> >=1.2.3 <2.0.0
//	^0.2.3 -> >=0.2.3 <0.3.0
//	^0.0.3 -> >=0.0.3 <0.0.4
//	^1.2   -> >=1.2.0 <2.0.0
//	^0     -> >=0.0.0 <1.0.0
func caretBounds(s string) (lo, hi Version, ok bool) {
	p, parsed := parsePartial(s)
	if !parsed {
		return Version{}, Version{}, false
	}
	lo = Version{Major: p.major, Minor: p.minor, Patch: p.patch, Prerelease: p.pre, Build: p.build}
	switch {
	case p.major != 0:
		hi = Version{Major: p.major + 1}
	case p.minor != 0:
		hi = Version{Major: 0, Minor: p.minor + 1}
	case p.specified >= 3:
		// ^0.0.3 -> <0.0.4 (only patch may move)
		hi = Version{Major: 0, Minor: 0, Patch: p.patch + 1}
	case p.specified == 2:
		// ^0.0 -> <0.1.0
		hi = Version{Major: 0, Minor: 1}
	default:
		// ^0 -> <1.0.0
		hi = Version{Major: 1}
	}
	return lo, hi, true
}

// tildeBounds returns the [lo, hi) bounds for a tilde range:
//
//	~1.2.3 -> >=1.2.3 <1.3.0
//	~1.2   -> >=1.2.0 <1.3.0
//	~1     -> >=1.0.0 <2.0.0
func tildeBounds(s string) (lo, hi Version, ok bool) {
	p, parsed := parsePartial(s)
	if !parsed {
		return Version{}, Version{}, false
	}
	lo = Version{Major: p.major, Minor: p.minor, Patch: p.patch, Prerelease: p.pre, Build: p.build}
	if p.specified >= 2 {
		// minor pinned: only patch may move.
		hi = Version{Major: p.major, Minor: p.minor + 1}
	} else {
		// only major pinned: minor may move.
		hi = Version{Major: p.major + 1}
	}
	return lo, hi, true
}
