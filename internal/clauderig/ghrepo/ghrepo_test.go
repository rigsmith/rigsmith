package ghrepo

import (
	"context"
	"errors"
	"strings"
	"testing"
)

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

func TestParseRemote_HostDispatch(t *testing.T) {
	cases := map[string]struct{ host, slug string }{
		"git@github.com:john/x.git":         {"github.com", "john/x"},
		"https://gitlab.com/grp/sub/x.git":  {"gitlab.com", "grp/sub/x"}, // GitLab subgroup
		"git@gitlab.com:john/x":             {"gitlab.com", "john/x"},
		"ssh://git@github.com/org/repo.git": {"github.com", "org/repo"},
	}
	for in, want := range cases {
		host, slug, ok := parseRemote(in)
		if !ok || host != want.host || slug != want.slug {
			t.Errorf("parseRemote(%q) = (%q,%q,%v), want (%q,%q)", in, host, slug, ok, want.host, want.slug)
		}
	}
	for _, in := range []string{"/local/path", "git@host", "https://bitbucket.org/a"} {
		if _, _, ok := parseRemote(in); ok {
			t.Errorf("parseRemote(%q) should be rejected", in)
		}
	}
}

// verify is the whole enforcement gate — the single decision that stops a
// backup reaching a public repo — and this package's tests covered only URL
// parsing. Both refusals could be deleted with the suite green, and deleting
// the second one is the difference between "no exceptions" and "publishes your
// transcripts to the internet".
func TestVerify_RefusesAnythingButAConfirmedPrivateRepo(t *testing.T) {
	yes := func(context.Context, string) (bool, error) { return true, nil }
	no := func(context.Context, string) (bool, error) { return false, nil }
	broken := func(context.Context, string) (bool, error) { return false, errors.New("not logged in") }
	// A checker that errors AND claims private: an error must win, so an
	// unverifiable repo is never taken on the value beside it.
	confused := func(context.Context, string) (bool, error) { return true, errors.New("404") }

	if err := verify(t.Context(), "me/backup", yes); err != nil {
		t.Errorf("a confirmed private repo was refused: %v", err)
	}
	for _, tc := range []struct {
		name  string
		check func(context.Context, string) (bool, error)
		want  string
	}{
		{"public", no, "not private"},
		{"unverifiable", broken, "could not verify"},
		{"error beats a private answer", confused, "could not verify"},
	} {
		err := verify(t.Context(), "me/backup", tc.check)
		if err == nil {
			t.Errorf("%s: accepted", tc.name)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error %q does not mention %q", tc.name, err, tc.want)
		}
	}
}

// A host clauderig cannot verify is refused rather than waved through. The
// parsing test covers which host a URL resolves to; this covers what happens to
// one that resolves to neither.
func TestEnsurePrivate_RefusesAnUnsupportedHost(t *testing.T) {
	for _, remote := range []string{
		"https://git.example.com/me/backup.git",
		"git@bitbucket.org:me/backup.git",
		"/srv/git/backup.git",
		"",
	} {
		if err := EnsurePrivate(t.Context(), remote); err == nil {
			t.Errorf("EnsurePrivate(%q) = nil, want a refusal", remote)
		}
	}
}
