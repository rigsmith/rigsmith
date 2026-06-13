// Ported from net-changesets Commands/Version/Helpers/ChangelogCommitResolver.cs.
package changelog

import (
	"strconv"
	"strings"

	"github.com/rigsmith/core/plugin"
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

// ResolveFromCommits builds CommitInfo for changesets whose source commit is
// already known — the commit-based versioning case, where the commit IS the
// provenance, so no `--diff-filter=A` archaeology is needed. idToSHA maps each
// changeset id to its full source SHA. For the github generator with a
// configured repo it still looks up the PR number and author via `gh api`
// (degrading each to a zero value on failure). The default generator returns an
// empty map. An empty SHA is skipped.
func ResolveFromCommits(idToSHA map[string]string, setting Setting, dir string, run Runner) map[string]CommitInfo {
	result := map[string]CommitInfo{}
	if setting.Kind == KindDefault {
		return result
	}
	for id, sha := range idToSHA {
		if sha == "" {
			continue
		}
		info := CommitInfo{Commit: shortSHA(sha)}
		if setting.Kind == KindGitHub && setting.Repo != "" {
			info.PullRequest = pullRequestForCommit(run, dir, setting.Repo, sha)
			info.Author = authorForCommit(run, dir, setting.Repo, sha)
		}
		result[id] = info
	}
	return result
}

// ResolveAuthors finds the author of the provenance commit behind each
// changeset id, for the "Contributors" section. knownSHA supplies the source
// commit for commit-derived changesets (where the commit IS the provenance);
// for on-disk changesets the commit that added the file is looked up. Each SHA
// is resolved once. The git author name/email always resolves (offline); when a
// repo slug is given, the GitHub login is additionally resolved via `gh api` so
// the contributor can be linked to their GitHub page (failure leaves it empty,
// rendering the bare name). An id whose author can't be resolved is omitted.
func ResolveAuthors(ids []string, knownSHA map[string]string, repo, dir string, run Runner) map[string]plugin.Author {
	result := map[string]plugin.Author{}
	bySHA := map[string]plugin.Author{}
	for _, id := range ids {
		sha := knownSHA[id]
		if sha == "" {
			sha = commitThatAddedChangeset(run, dir, id, "%H")
		}
		if sha == "" {
			continue
		}
		a, ok := bySHA[sha]
		if !ok {
			a = authorOfCommit(run, dir, sha)
			if repo != "" {
				if login := authorForCommit(run, dir, repo, sha); login != "" {
					a.Login = login
				}
			}
			bySHA[sha] = a
		}
		if a.Name == "" && a.Login == "" {
			continue
		}
		result[id] = a
	}
	return result
}

// authorOfCommit reads the git author name and email of a commit. Email is used
// only for de-duplication and exclude-matching; it is never rendered.
func authorOfCommit(run Runner, dir, sha string) plugin.Author {
	out, err := run(dir, "git", "show", "-s", "--format=%an\x1f%ae", sha)
	if err != nil {
		return plugin.Author{}
	}
	line := strings.TrimSpace(out)
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	parts := strings.SplitN(line, "\x1f", 2)
	a := plugin.Author{Name: strings.TrimSpace(parts[0])}
	if len(parts) == 2 {
		a.Email = strings.TrimSpace(parts[1])
	}
	return a
}

// shortSHA abbreviates a full commit SHA to git's conventional 7 characters,
// matching the `%h` abbreviation the file-based resolver uses.
func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
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
