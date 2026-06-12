package ghrepo

import "testing"

func TestParseSlug(t *testing.T) {
	ok := map[string][2]string{
		"git@github.com:john/claude-sync.git":       {"john", "claude-sync"},
		"git@github.com:john/claude-sync":           {"john", "claude-sync"},
		"https://github.com/john/claude-sync.git":   {"john", "claude-sync"},
		"https://github.com/john/claude-sync":       {"john", "claude-sync"},
		"ssh://git@github.com/john/claude-sync.git": {"john", "claude-sync"},
		"  git@github.com:Org-Name/repo.git  ":      {"Org-Name", "repo"},
	}
	for in, want := range ok {
		o, r, valid := ParseSlug(in)
		if !valid || o != want[0] || r != want[1] {
			t.Errorf("ParseSlug(%q) = (%q,%q,%v), want (%q,%q,true)", in, o, r, valid, want[0], want[1])
		}
	}

	reject := []string{
		"git@gitlab.com:john/repo.git",    // non-github
		"https://bitbucket.org/john/repo", // non-github
		"git@github.com:john",             // no repo
		"https://github.com/john/",        // empty repo
		"/local/path",                     // not a url
		"git@github.com:a/b/c.git",        // too many segments
	}
	for _, in := range reject {
		if _, _, ok := ParseSlug(in); ok {
			t.Errorf("ParseSlug(%q) should be rejected", in)
		}
	}
}
