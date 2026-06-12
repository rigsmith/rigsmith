// Ported from net-changesets Commands/Version/Helpers/ChangelogCommitResolver.cs.
package changelog

import (
	"strconv"
	"strings"
)

// Runner executes a command in dir and returns its combined stdout. A spawn
// failure or non-zero exit must be reported as a non-nil error; the resolver
// degrades any such failure to a missing field rather than surfacing it.
type Runner func(dir, name string, args ...string) (string, error)

// CommitInfo is the version-control facts about a changeset used by the
// git/github changelog generators: the short commit that added it, and (for
// github) the pull request number and author login. Zero values (empty string,
// 0) mean the fact could not be resolved.
type CommitInfo struct {
	Commit      string
	PullRequest int
	Author      string
}

// changesetExtensions are the changeset file extensions tried, in order, when
// locating the commit that added a changeset (the C# tool's [".md", ".net.mkd"]).
var changesetExtensions = []string{".md", ".net.mkd"}

// Resolve finds, for each changeset id, the commit that added the changeset
// file (`git log --diff-filter=A`); for the github generator with a configured
// repo it additionally looks up the associated pull request and author via
// `gh api`. The default generator performs no process calls and returns an
// empty map. Any lookup that fails (no git history, gh not installed or
// unauthenticated) yields a zero field, so the changelog degrades gracefully
// rather than erroring; a changeset whose commit cannot be found is omitted.
func Resolve(changesetIDs []string, setting Setting, dir string, run Runner) map[string]CommitInfo {
	result := map[string]CommitInfo{}

	if setting.Kind == KindDefault {
		return result
	}

	seen := map[string]bool{}
	for _, id := range changesetIDs {
		if seen[id] {
			continue
		}
		seen[id] = true

		shortHash := commitThatAddedChangeset(run, dir, id, "%h")
		if shortHash == "" {
			continue
		}

		info := CommitInfo{Commit: shortHash}
		if setting.Kind == KindGitHub && setting.Repo != "" {
			fullHash := commitThatAddedChangeset(run, dir, id, "%H")
			if fullHash != "" {
				info.PullRequest = pullRequestForCommit(run, dir, setting.Repo, fullHash)
				info.Author = authorForCommit(run, dir, setting.Repo, fullHash)
			}
		}

		result[id] = info
	}

	return result
}

func commitThatAddedChangeset(run Runner, dir, id, format string) string {
	for _, extension := range changesetExtensions {
		hash := runFirstLine(run, dir,
			"git", "log", "--diff-filter=A", "--max-count=1", "--format="+format, "--", ".changeset/"+id+extension)
		if hash != "" {
			return hash
		}
	}
	return ""
}

func pullRequestForCommit(run Runner, dir, repo, sha string) int {
	value := runFirstLine(run, dir, "gh", "api", "repos/"+repo+"/commits/"+sha+"/pulls", "--jq", ".[0].number")
	number, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}
	return number
}

func authorForCommit(run Runner, dir, repo, sha string) string {
	return runFirstLine(run, dir, "gh", "api", "repos/"+repo+"/commits/"+sha, "--jq", ".author.login")
}

// runFirstLine runs the command and returns the first trimmed non-empty output
// line that isn't the literal "null" (gh's jq prints "null" for absent JSON
// fields). Any failure — spawn error or non-zero exit — yields "".
func runFirstLine(run Runner, dir, name string, args ...string) string {
	output, err := run(dir, name, args...)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && line != "null" {
			return line
		}
	}
	return ""
}
