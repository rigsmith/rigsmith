// Package issuerefs extracts issue references from a release's commit messages,
// so the release `issues` step can comment on / close the issues a release
// resolves. It is a pure parser: callers gather the released commit range (e.g.
// gitutil.LogSince over the same range the changelog uses) and pass the messages
// in; nothing here touches git or the network.
//
// Two ref namespaces are recognized:
//   - Forge issues — `#123` (GitHub / Gitea), same-repo only.
//   - Jira issues — `KEY-123`, for the configured project keys.
//
// A ref is "closing" when a closing keyword (close/fix/resolve and their
// inflections) immediately precedes it — those are eligible to be closed; a bare
// reference is a mention (comment-only).
package issuerefs

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Kind is the ref namespace.
type Kind int

const (
	// Forge is a `#123` issue on the release's forge (GitHub/Gitea).
	Forge Kind = iota
	// Jira is a `KEY-123` issue.
	Jira
)

// String returns the kind's lowercase name.
func (k Kind) String() string {
	if k == Jira {
		return "jira"
	}
	return "forge"
}

// Ref is one deduplicated issue reference.
type Ref struct {
	// ID is "123" for a forge ref or "ENG-45" for a Jira ref.
	ID string
	// Kind is the ref namespace.
	Kind Kind
	// Closing is true when at least one occurrence was preceded by a closing
	// keyword (the ref is eligible to be closed, not just commented).
	Closing bool
}

// closingKeyword matches GitHub/Gitea closing keywords (case-insensitive),
// reused for both forge and Jira closing detection. The leading `\b` keeps a
// keyword glued inside a larger word from matching (e.g. the "fix" in "prefix").
// Kept as a fragment so it can be embedded into the larger patterns with the
// namespace-specific suffix.
const closingKeyword = `\b(?i:close[sd]?|fix(?:e[sd])?|resolve[sd]?)\b[\s:]+`

var (
	// forgeRe matches a `#123` not glued to a preceding word char (so "abc#1"
	// and Markdown headings don't match). Group 2 is the number.
	forgeRe = regexp.MustCompile(`(^|[^\w])#(\d+)`)
	// closingForgeRe matches a closing keyword immediately before a `#123`.
	closingForgeRe = regexp.MustCompile(closingKeyword + `#(\d+)`)
)

// Collect parses forge refs (`#123`) and Jira refs (`KEY-123`, for the given
// project keys) out of the commit messages, deduplicated by (Kind, ID). A ref's
// Closing flag is the OR across all its occurrences. The result is sorted for
// stable output: forge refs (by number) first, then Jira refs (by key then
// number). jiraProjects may be empty (no Jira scanning).
func Collect(messages []string, jiraProjects []string) []Ref {
	type key struct {
		kind Kind
		id   string
	}
	closing := map[key]bool{} // accumulates Closing across occurrences

	mark := func(k Kind, id string, isClosing bool) {
		kk := key{k, id}
		closing[kk] = closing[kk] || isClosing
	}

	jiraRe, closingJiraRe := jiraPatterns(jiraProjects)

	for _, msg := range messages {
		for _, m := range forgeRe.FindAllStringSubmatch(msg, -1) {
			mark(Forge, m[2], false)
		}
		for _, m := range closingForgeRe.FindAllStringSubmatch(msg, -1) {
			mark(Forge, m[1], true)
		}
		if jiraRe != nil {
			for _, m := range jiraRe.FindAllStringSubmatch(msg, -1) {
				mark(Jira, m[1], false)
			}
			for _, m := range closingJiraRe.FindAllStringSubmatch(msg, -1) {
				mark(Jira, m[1], true)
			}
		}
	}

	refs := make([]Ref, 0, len(closing))
	for kk, isClosing := range closing {
		refs = append(refs, Ref{ID: kk.id, Kind: kk.kind, Closing: isClosing})
	}
	sortRefs(refs)
	return refs
}

// jiraPatterns builds the Jira ref regexes from the project keys, or (nil, nil)
// when there are no keys. The key alternation is case-sensitive (Jira keys are
// uppercase); only the closing keyword is case-insensitive.
func jiraPatterns(projects []string) (all, closingPat *regexp.Regexp) {
	keys := make([]string, 0, len(projects))
	for _, p := range projects {
		if p = strings.TrimSpace(p); p != "" {
			keys = append(keys, regexp.QuoteMeta(p))
		}
	}
	if len(keys) == 0 {
		return nil, nil
	}
	alt := `((?:` + strings.Join(keys, "|") + `)-\d+)\b`
	return regexp.MustCompile(`\b` + alt), regexp.MustCompile(closingKeyword + alt)
}

// sortRefs orders refs deterministically: forge before Jira; forge by numeric
// value; Jira lexically by ID.
func sortRefs(refs []Ref) {
	sort.Slice(refs, func(i, j int) bool {
		a, b := refs[i], refs[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Kind == Forge {
			ai, _ := strconv.Atoi(a.ID)
			bi, _ := strconv.Atoi(b.ID)
			return ai < bi
		}
		return a.ID < b.ID
	})
}
