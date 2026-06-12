// Ported from net-changesets Version/ChangelogCommitResolverTests.cs, with a
// fake Runner standing in for the mocked IProcessExecutor (no real git/gh).
package changelog

import (
	"errors"
	"reflect"
	"strings"
	"testing"
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
