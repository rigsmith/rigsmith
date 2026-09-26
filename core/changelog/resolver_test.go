// Ported from net-changesets Version/ChangelogCommitResolverTests.cs, with a
// fake Runner standing in for the mocked IProcessExecutor (no real git/gh).
package changelog

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/plugin"
)

// fakeRunner matches invocations the way the C# tests do: by executable name
// plus a marker substring of the arguments, returning canned output. Unmatched
// invocations fail (non-zero exit / spawn error equivalent).
type fakeRunner struct {
	calls     int
	responses []fakeResponse
}

type fakeResponse struct {
	name   string
	marker string
	output string
	err    error
}

func (f *fakeRunner) run(dir, name string, args ...string) (string, error) {
	f.calls++
	joined := strings.Join(args, " ")
	for _, r := range f.responses {
		if r.name == name && strings.Contains(joined, r.marker) {
			return r.output, r.err
		}
	}
	return "", errors.New("exit status 1")
}

func TestResolveDefaultGeneratorDoesNotTouchGitAndReturnsEmpty(t *testing.T) {
	runner := &fakeRunner{}

	result := Resolve([]string{"cs1"}, Setting{Kind: KindDefault}, "/repo", runner.run)

	if len(result) != 0 {
		t.Errorf("Resolve() = %v, want empty", result)
	}
	if runner.calls != 0 {
		t.Errorf("runner called %d times, want 0", runner.calls)
	}
}

func TestResolveGitResolvesTheCommitThatAddedTheChangeset(t *testing.T) {
	runner := &fakeRunner{responses: []fakeResponse{
		{name: "git", marker: "--abbrev=7 --format=%H %h", output: "abc1234567890 abc1234:0f1e2d3c4b5a6"},
	}}

	result := Resolve([]string{"cs1"}, Setting{Kind: KindGit}, "/repo", runner.run)

	want := CommitInfo{Commit: "abc1234567890", Short: "abc1234"}
	if got := result["cs1"]; got != want {
		t.Errorf("result[cs1] = %+v, want %+v", got, want)
	}
}

func TestResolveGitNoCommitFoundSkipsTheChangeset(t *testing.T) {
	runner := &fakeRunner{} // every invocation fails

	result := Resolve([]string{"cs1"}, Setting{Kind: KindGit}, "/repo", runner.run)

	if _, ok := result["cs1"]; ok {
		t.Errorf("result contains cs1 (%+v), want it omitted", result["cs1"])
	}
}

func TestResolveGitHubResolvesCommitPullRequestAndAuthor(t *testing.T) {
	runner := &fakeRunner{responses: []fakeResponse{
		{name: "git", marker: "--abbrev=7 --format=%H %h", output: "abc1234567890 abc1234:0f1e2d3c4b5a6"},
		{name: "gh", marker: "commits/abc1234567890/pulls", output: "42"},
		{name: "gh", marker: ".author.login", output: "octocat"},
	}}

	result := Resolve([]string{"cs1"}, Setting{Kind: KindGitHub, Repo: "acme/widgets"}, "/repo", runner.run)

	want := map[string]CommitInfo{"cs1": {Commit: "abc1234567890", Short: "abc1234", PullRequest: 42, Author: "octocat"}}
	if !reflect.DeepEqual(result, want) {
		t.Errorf("Resolve() = %v, want %v", result, want)
	}
}

func TestResolveGitHubWhenGhFailsKeepsTheCommitButLeavesPrAndAuthorZero(t *testing.T) {
	runner := &fakeRunner{responses: []fakeResponse{
		{name: "git", marker: "--abbrev=7 --format=%H %h", output: "abc1234567890 abc1234:0f1e2d3c4b5a6"},
		{name: "gh", marker: "api", output: "gh: not authenticated", err: errors.New("exit status 1")},
	}}

	result := Resolve([]string{"cs1"}, Setting{Kind: KindGitHub, Repo: "acme/widgets"}, "/repo", runner.run)

	want := CommitInfo{Commit: "abc1234567890", Short: "abc1234"}
	if got := result["cs1"]; got != want {
		t.Errorf("result[cs1] = %+v, want %+v", got, want)
	}
}

// Behaviors below come from the C# implementation rather than its test suite.

func TestResolveFallsBackToTheNetMkdExtension(t *testing.T) {
	runner := &fakeRunner{responses: []fakeResponse{
		{name: "git", marker: ".changeset/cs1.net.mkd", output: "abc1234567890 abc1234:0f1e2d3c4b5a6"},
	}}

	result := Resolve([]string{"cs1"}, Setting{Kind: KindGit}, "/repo", runner.run)

	want := CommitInfo{Commit: "abc1234567890", Short: "abc1234"}
	if got := result["cs1"]; got != want {
		t.Errorf("result[cs1] = %+v, want %+v", got, want)
	}
}

func TestResolveGitHubTreatsTheNullLiteralAsMissing(t *testing.T) {
	runner := &fakeRunner{responses: []fakeResponse{
		{name: "git", marker: "--abbrev=7 --format=%H %h", output: "abc1234567890 abc1234:0f1e2d3c4b5a6"},
		// gh's --jq prints the literal "null" when the JSON field is absent.
		{name: "gh", marker: "api", output: "null\n"},
	}}

	result := Resolve([]string{"cs1"}, Setting{Kind: KindGitHub, Repo: "acme/widgets"}, "/repo", runner.run)

	want := CommitInfo{Commit: "abc1234567890", Short: "abc1234"}
	if got := result["cs1"]; got != want {
		t.Errorf("result[cs1] = %+v, want %+v", got, want)
	}
}

func TestResolveFromCommitsDefaultGeneratorIsEmptyAndSilent(t *testing.T) {
	runner := &fakeRunner{}
	result := ResolveFromCommits(map[string]string{"abc1234": "abc1234567890"}, Setting{Kind: KindDefault}, "/repo", runner.run)
	if len(result) != 0 {
		t.Errorf("ResolveFromCommits() = %v, want empty", result)
	}
	if runner.calls != 0 {
		t.Errorf("runner called %d times, want 0", runner.calls)
	}
}

func TestResolveFromCommitsGitUsesTheKnownShaWithoutArchaeology(t *testing.T) {
	runner := &fakeRunner{responses: []fakeResponse{
		{name: "git", marker: "--no-walk=unsorted --abbrev=7 --format=%H %h abc1234567890", output: "abc1234567890 abc1234\n"},
	}}
	result := ResolveFromCommits(map[string]string{"abc1234": "abc1234567890"}, Setting{Kind: KindGit}, "/repo", runner.run)

	want := CommitInfo{Commit: "abc1234567890", Short: "abc1234"}
	if got := result["abc1234"]; got != want {
		t.Errorf("result = %+v, want %+v", got, want)
	}
	// One git log for the display form, and no --diff-filter=A archaeology:
	// the SHA is already known.
	if runner.calls != 1 {
		t.Errorf("git mode made %d calls, want 1 (the abbreviation lookup)", runner.calls)
	}
}

// Every SHA is abbreviated in a single git log, and each gets its own line.
func TestResolveFromCommitsAbbreviatesEveryShaInOneCall(t *testing.T) {
	runner := &fakeRunner{responses: []fakeResponse{
		{name: "git", marker: "--format=%H %h aaa1111222 bbb2222333", output: "bbb2222333 bbb2222\naaa1111222 aaa1111\n"},
	}}
	result := ResolveFromCommits(map[string]string{"b": "bbb2222333", "a": "aaa1111222"}, Setting{Kind: KindGit}, "/repo", runner.run)

	want := map[string]CommitInfo{
		"a": {Commit: "aaa1111222", Short: "aaa1111"},
		"b": {Commit: "bbb2222333", Short: "bbb2222"},
	}
	if !reflect.DeepEqual(result, want) {
		t.Errorf("result = %+v, want %+v", result, want)
	}
	if runner.calls != 1 {
		t.Errorf("made %d calls, want 1", runner.calls)
	}
}

// A SHA git can't abbreviate (not in this repository) fails the batched
// git log; the others still get theirs, and it shows as its first 7
// characters, as @changesets shows every commit.
func TestResolveFromCommitsUnknownShaFallsBackToSevenCharacters(t *testing.T) {
	runner := &fakeRunner{responses: []fakeResponse{
		{name: "git", marker: "--format=%H %h aaa1111222 bbb2222333", err: errors.New("exit status 128")},
		{name: "git", marker: "rev-parse --short=7 aaa1111222", output: "aaa11112\n"},
	}}
	result := ResolveFromCommits(map[string]string{"a": "aaa1111222", "b": "bbb2222333"}, Setting{Kind: KindGit}, "/repo", runner.run)

	if got := result["a"]; got.Short != "aaa11112" || got.Display() != "aaa11112" {
		t.Errorf("a = %+v (display %q), want the per-SHA abbreviation aaa11112", got, got.Display())
	}
	if got := result["b"]; got.Commit != "bbb2222333" || got.Short != "" || got.Display() != "bbb2222" {
		t.Errorf("b = %+v (display %q), want the full SHA with display bbb2222", got, got.Display())
	}
}

func TestResolveFromCommitsGitHubLooksUpPrAndAuthorFromTheSha(t *testing.T) {
	runner := &fakeRunner{responses: []fakeResponse{
		{name: "git", marker: "--no-walk=unsorted --abbrev=7 --format=%H %h abc1234567890", output: "abc1234567890 abc1234\n"},
		{name: "gh", marker: "commits/abc1234567890/pulls", output: "42"},
		{name: "gh", marker: ".author.login", output: "octocat"},
	}}
	result := ResolveFromCommits(map[string]string{"abc1234": "abc1234567890"}, Setting{Kind: KindGitHub, Repo: "acme/widgets"}, "/repo", runner.run)

	want := CommitInfo{Commit: "abc1234567890", Short: "abc1234", PullRequest: 42, Author: "octocat"}
	if got := result["abc1234"]; got != want {
		t.Errorf("result = %+v, want %+v", got, want)
	}
}

func TestResolveFromCommitsSkipsEmptySha(t *testing.T) {
	runner := &fakeRunner{}
	result := ResolveFromCommits(map[string]string{"x": ""}, Setting{Kind: KindGit}, "/repo", runner.run)
	if len(result) != 0 {
		t.Errorf("empty SHA should be skipped, got %v", result)
	}
}

func TestResolveAuthorsCommitModeUsesKnownSha(t *testing.T) {
	runner := &fakeRunner{responses: []fakeResponse{
		{name: "git", marker: "show", output: "Pooya Parsa\x1fpooya@pi0.io\x1f"},
	}}
	// commit mode: the SHA is known, so no archaeology; no repo → no gh login.
	got := ResolveAuthors([]string{"abc1234"}, map[string]string{"abc1234": "abc1234567890"}, "", "/repo", runner.run)
	want := []plugin.Author{{Name: "Pooya Parsa", Email: "pooya@pi0.io"}}
	if !reflect.DeepEqual(got["abc1234"], want) {
		t.Errorf("ResolveAuthors = %+v, want %+v", got["abc1234"], want)
	}
}

func TestResolveAuthorsFileModeLooksUpAddingCommitAndLogin(t *testing.T) {
	runner := &fakeRunner{responses: []fakeResponse{
		// file mode: find the commit that added the changeset, then read its author.
		{name: "git", marker: "--diff-filter=A", output: "deadbee1234567:0f1e2d3c4b5a6"},
		{name: "git", marker: "show", output: "Jane Doe\x1fjane@example.com\x1f"},
		{name: "gh", marker: ".author.login", output: "janedoe"},
	}}
	got := ResolveAuthors([]string{"brave-otters-dance"}, nil, "acme/widgets", "/repo", runner.run)
	want := []plugin.Author{{Name: "Jane Doe", Email: "jane@example.com", Login: "janedoe"}}
	if !reflect.DeepEqual(got["brave-otters-dance"], want) {
		t.Errorf("ResolveAuthors = %+v, want %+v", got["brave-otters-dance"], want)
	}
}

func TestResolveAuthorsIncludesCoAuthors(t *testing.T) {
	body := "Some body text.\n\nCo-authored-by: Pooya Parsa <pooya@pi0.io>\nco-authored-by: Bob <bob@example.com>"
	runner := &fakeRunner{responses: []fakeResponse{
		{name: "git", marker: "show", output: "Jannchie\x1fjannchie@gmail.com\x1f" + body},
		{name: "gh", marker: ".author.login", output: "jannchie"}, // login only on the commit author
	}}
	got := ResolveAuthors([]string{"id"}, map[string]string{"id": "abc1234567890"}, "acme/widgets", "/repo", runner.run)
	want := []plugin.Author{
		{Name: "Jannchie", Email: "jannchie@gmail.com", Login: "jannchie"},
		{Name: "Pooya Parsa", Email: "pooya@pi0.io"},
		{Name: "Bob", Email: "bob@example.com"},
	}
	if !reflect.DeepEqual(got["id"], want) {
		t.Errorf("ResolveAuthors with co-authors = %+v, want %+v", got["id"], want)
	}
}

func TestResolveAuthorsSkipsUnresolvable(t *testing.T) {
	runner := &fakeRunner{} // every call fails
	got := ResolveAuthors([]string{"x"}, nil, "", "/repo", runner.run)
	if len(got) != 0 {
		t.Errorf("unresolvable author should be omitted, got %v", got)
	}
}
