package detect

import (
	"regexp"
	"strings"
)

// GlobMatch is minimal, dependency-free glob matching for config patterns
// (matching the .NET rig's Glob): '*' matches any run of characters, '?' a
// single one. Case-insensitive, anchored (the whole input must match). Used by
// `exclude` project filtering.
func GlobMatch(pattern, input string) bool {
	var b strings.Builder
	b.WriteString("(?i)^")
	for _, r := range pattern {
		switch r {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteString(".")
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	return err == nil && re.MatchString(input)
}
