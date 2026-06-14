// Package gitignore is a minimal, idempotent editor for .gitignore content: add a
// line if it isn't already there, leaving the rest untouched. clauderig uses it so
// `local install` can keep a personal settings.local.json out of version control.
package gitignore

import "strings"

// EnsureLine returns content with entry present as its own line, and whether it
// had to be added. An existing exact line (ignoring surrounding whitespace) is
// left as-is, so repeated calls are no-ops.
func EnsureLine(content, entry string) (string, bool) {
	for _, line := range strings.Split(content, "\n") {
		if strings.TrimSpace(line) == entry {
			return content, false
		}
	}
	var b strings.Builder
	b.WriteString(content)
	if content != "" && !strings.HasSuffix(content, "\n") {
		b.WriteString("\n")
	}
	b.WriteString(entry + "\n")
	return b.String(), true
}
