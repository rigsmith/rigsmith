package commitsource

import "testing"

// Parity fixtures lifted from @unjs/changelogen's test/git.test.ts (the
// parseCommits cases). We model a subset of changelogen's GitCommit — type,
// scope, breaking, and the cleaned description — and ignore the fields that
// belong to a different seam in rigsmith (references/authors are handled by the
// changelog provenance enrichment, not the commit parser). Each case asserts
// what changelogen's parser produces for those four fields.
type clogenCase struct {
	subject      string
	body         string
	wantType     string
	wantScope    string
	wantBreaking bool
	wantDesc     string
}

var changelogenCases = []clogenCase{
	// Emoji and :shortcode: prefixes before the conventional type.
	{"🚀 feat: add emoji support", "this is a emoji commit", "feat", "", false, "add emoji support"},
	{":bug: fix: this is a text emoji", "", "fix", "", false, "this is a text emoji"},
	{":bug: fix(scope): this is a text emoji with scope", "", "fix", "scope", false, "this is a text emoji with scope"},
	// Breaking via a BREAKING CHANGE body, with an emoji prefix on the subject.
	{"💥 feat: added a breaking change", "BREAKING CHANGE: added a breaking change.", "feat", "", true, "added a breaking change"},
	// The "parse" snapshot commits.
	{"chore(release): v0.3.5", "", "chore", "release", false, "v0.3.5"},
	{"fix(scope)!: breaking change example, close #123 (#134)", "", "fix", "scope", true, "breaking change example, close #123"},
	{"feat: infer github config from package.json (resolves #37)", "", "feat", "", false, "infer github config from package.json"},
	{"fix: consider docs and refactor as semver patch for bump", "", "fix", "", false, "consider docs and refactor as semver patch for bump"},
	{"chore: fix typecheck", "", "chore", "", false, "fix typecheck"},
	{"chore: update dependencies", "", "chore", "", false, "update dependencies"},
	{"chore(deps): update all non-major dependencies (#42)", "", "chore", "deps", false, "update all non-major dependencies"},
}

func TestChangelogenParserParity(t *testing.T) {
	for _, c := range changelogenCases {
		h, ok := parseHeader(c.subject)
		if !ok {
			t.Errorf("%q: parseHeader returned ok=false (changelogen parses this)", c.subject)
			continue
		}
		breaking := h.breaking || breakingFooterRe.MatchString(c.body)
		if h.typ != c.wantType {
			t.Errorf("%q: type = %q, want %q", c.subject, h.typ, c.wantType)
		}
		if h.scope != c.wantScope {
			t.Errorf("%q: scope = %q, want %q", c.subject, h.scope, c.wantScope)
		}
		if breaking != c.wantBreaking {
			t.Errorf("%q: breaking = %v, want %v", c.subject, breaking, c.wantBreaking)
		}
		if h.desc != c.wantDesc {
			t.Errorf("%q: desc = %q, want %q", c.subject, h.desc, c.wantDesc)
		}
	}
}
