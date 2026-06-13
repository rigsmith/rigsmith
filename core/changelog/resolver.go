// Ported from net-changesets Commands/Version/Helpers/ChangelogCommitResolver.cs.
package changelog

import (
	"regexp"
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

// ResolveAuthors finds the authors of the provenance commit behind each
// changeset id, for the "Contributors" section: the commit author first, then
// any `Co-authored-by:` trailers. knownSHA supplies the source commit for
// commit-derived changesets (where the commit IS the provenance); for on-disk
// changesets the commit that added the file is looked up. Each SHA is resolved
// once. Names/emails always resolve (offline); when a repo slug is given the
// commit author's GitHub login is additionally resolved via `gh api` so they can
// be linked to their GitHub page (co-author logins are not resolved — they
// render as bare names, or pick up a login when they appear as the author of
// another commit and merge by email). An id with no resolvable author is omitted.
func ResolveAuthors(ids []string, knownSHA map[string]string, repo, dir string, run Runner) map[string][]plugin.Author {
	result := map[string][]plugin.Author{}
	bySHA := map[string][]plugin.Author{}
	for _, id := range ids {
		sha := knownSHA[id]
		if sha == "" {
			sha = commitThatAddedChangeset(run, dir, id, "%H")
		}
		if sha == "" {
			continue
		}
		authors, ok := bySHA[sha]
		if !ok {
			authors = authorsOfCommit(run, dir, sha)
			if repo != "" && len(authors) > 0 {
				if login := authorForCommit(run, dir, repo, sha); login != "" {
					authors[0].Login = login // the commit author is first
				}
			}
			bySHA[sha] = authors
		}
		if len(authors) > 0 {
			result[id] = authors
		}
	}
	return result
}

// coAuthorRe matches a `Co-authored-by: Name <email>` trailer (one per line,
// case-insensitive), mirroring changelogen's CoAuthoredByRegex.
var coAuthorRe = regexp.MustCompile(`(?im)^\s*co-authored-by:\s*(.+?)\s*<(.+?)>\s*$`)

// authorsOfCommit reads a commit's author and its Co-authored-by trailers. The
// commit author is returned first. Emails are used only for de-duplication and
// exclude-matching; they are never rendered.
func authorsOfCommit(run Runner, dir, sha string) []plugin.Author {
	// %b (the body) is last so it can contain newlines without breaking the split.
	out, err := run(dir, "git", "show", "-s", "--format=%an\x1f%ae\x1f%b", sha)
	if err != nil {
		return nil
	}
	parts := strings.SplitN(strings.TrimRight(out, "\n"), "\x1f", 3)
	if len(parts) < 2 {
		return nil
	}
	var authors []plugin.Author
	if name, email := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]); name != "" || email != "" {
		authors = append(authors, plugin.Author{Name: name, Email: email})
	}
	if len(parts) == 3 {
		for _, m := range coAuthorRe.FindAllStringSubmatch(parts[2], -1) {
			name, email := strings.TrimSpace(m[1]), strings.TrimSpace(m[2])
			if name != "" || email != "" {
				authors = append(authors, plugin.Author{Name: name, Email: email})
			}
		}
	}
	return authors
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
