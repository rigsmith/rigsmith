package status

import "strings"

// squashRoots are the messages clauderig's own squashes write. A root commit
// carrying one of them means history was truncated there rather than started
// there — the automatic size-based squash does this without being asked, so the
// distinction is not a rare edge case.
var squashRoots = []string{
	"clauderig: squashed history",
	"clauderig: history before",
}

// SquashedRoot reports whether a root commit's subject is one of ours.
//
// Here rather than in the commands package, which is where it was first
// written. It is knowledge about the repository's shape, not about a verb, and
// every reader of it — the CLI's `repo`, the window's panel — was importing the
// whole cobra command tree to ask a question about a string.
func SquashedRoot(subject string) bool {
	for _, p := range squashRoots {
		if strings.HasPrefix(subject, p) {
			return true
		}
	}
	return false
}
