// Ported from net-changesets Version/ChangelogCommitResolverTests.cs, with a
// fake Runner standing in for the mocked IProcessExecutor (no real git/gh).
package changelog

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/rigsmith/core/plugin"
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

func TestResolveGitResolvesTheShortCommitThatAddedTheChangeset(t *testing.T) {
	runner := &fakeRunner{responses: []fakeResponse{
		{name: "git", marker: "--format=%h", output: "abc1234"},
	}}

	result := Resolve([]string{"cs1"}, Setting{Kind: KindGit}, "/repo", runner.run)

	want := CommitInfo{Commit: "abc1234"}
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
		{name: "git", marker: "--format=%h", output: "abc1234"},
		{name: "git", marker: "--format=%H", output: "abc1234567890"},
		{name: "gh", marker: "/pulls", output: "42"},
		{name: "gh", marker: ".author.login", output: "octocat"},
	}}

	result := Resolve([]string{"cs1"}, Setting{Kind: KindGitHub, Repo: "acme/widgets"}, "/repo", runner.run)

	want := map[string]CommitInfo{"cs1": {Commit: "abc1234", PullRequest: 42, Author: "octocat"}}
	if !reflect.DeepEqual(result, want) {
		t.Errorf("Resolve() = %v, want %v", result, want)
	}
}

func TestResolveGitHubWhenGhFailsKeepsTheCommitButLeavesPrAndAuthorZero(t *testing.T) {
	runner := &fakeRunner{responses: []fakeResponse{
		{name: "git", marker: "--format=%h", output: "abc1234"},
		{name: "git", marker: "--format=%H", output: "abc1234567890"},
		{name: "gh", marker: "api", output: "gh: not authenticated", err: errors.New("exit status 1")},
	}}

	result := Resolve([]string{"cs1"}, Setting{Kind: KindGitHub, Repo: "acme/widgets"}, "/repo", runner.run)

	want := CommitInfo{Commit: "abc1234"}
	if got := result["cs1"]; got != want {
		t.Errorf("result[cs1] = %+v, want %+v", got, want)
	}
}

// Behaviors below come from the C# implementation rather than its test suite.

func TestResolveFallsBackToTheNetMkdExtension(t *testing.T) {
	runner := &fakeRunner{responses: []fakeResponse{
		{name: "git", marker: ".changeset/cs1.net.mkd", output: "abc1234"},
	}}

	result := Resolve([]string{"cs1"}, Setting{Kind: KindGit}, "/repo", runner.run)

	want := CommitInfo{Commit: "abc1234"}
	if got := result["cs1"]; got != want {
		t.Errorf("result[cs1] = %+v, want %+v", got, want)
	}
}

func TestResolveGitHubTreatsTheNullLiteralAsMissing(t *testing.T) {
	runner := &fakeRunner{responses: []fakeResponse{
		{name: "git", marker: "--format=%h", output: "abc1234"},
		{name: "git", marker: "--format=%H", output: "abc1234567890"},
		// gh's --jq prints the literal "null" when the JSON field is absent.
		{name: "gh", marker: "api", output: "null\n"},
	}}

	result := Resolve([]string{"cs1"}, Setting{Kind: KindGitHub, Repo: "acme/widgets"}, "/repo", runner.run)

	want := CommitInfo{Commit: "abc1234"}
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
	runner := &fakeRunner{}
	result := ResolveFromCommits(map[string]string{"abc1234": "abc1234567890"}, Setting{Kind: KindGit}, "/repo", runner.run)

	want := CommitInfo{Commit: "abc1234"} // abbreviated to 7 chars, no git call
	if got := result["abc1234"]; got != want {
		t.Errorf("result = %+v, want %+v", got, want)
	}
	if runner.calls != 0 {
		t.Errorf("git mode made %d calls, want 0 (the SHA is already known)", runner.calls)
	}
}

func TestResolveFromCommitsGitHubLooksUpPrAndAuthorFromTheSha(t *testing.T) {
	runner := &fakeRunner{responses: []fakeResponse{
		{name: "gh", marker: "/pulls", output: "42"},
		{name: "gh", marker: ".author.login", output: "octocat"},
	}}
	result := ResolveFromCommits(map[string]string{"abc1234": "abc1234567890"}, Setting{Kind: KindGitHub, Repo: "acme/widgets"}, "/repo", runner.run)

	want := CommitInfo{Commit: "abc1234", PullRequest: 42, Author: "octocat"}
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
		{name: "git", marker: "--diff-filter=A", output: "deadbee1234567"},
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
