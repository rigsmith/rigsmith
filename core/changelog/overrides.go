package changelog

import (
	"regexp"
	"strconv"
	"strings"
)

// Overrides are the lines @changesets/changelog-github reads out of a
// changeset's summary to replace what it would look up: `pr: #12` (also
// `pull:` and `pull request:`), `commit: abc1234`, and any number of
// `author: @user` (or `user:`) lines.
type Overrides struct {
	PR     int
	Commit string
	Users  []string
}

// Any reports whether the summary named anything to override.
func (o Overrides) Any() bool {
	return o.PR != 0 || o.Commit != "" || len(o.Users) > 0
}

// The patterns changelog-github 1.0 uses, with its flags: the first pr: and
// commit: line count, every author: line does.
var (
	prOverrideRe     = regexp.MustCompile(`(?im)^\s*(?:pr|pull|pull\s+request):\s*#?(\d+)`)
	commitOverrideRe = regexp.MustCompile(`(?im)^\s*commit:\s*([^\s]+)`)
	userOverrideRe   = regexp.MustCompile(`(?im)^\s*(?:author|user):\s*@?([^\s]+)`)
)

// ExtractOverrides takes the override lines out of a summary, as
// changelog-github does before rendering it: each match is removed (only
// the matched part: text after it on the same line stays), then the summary
// is trimmed and each line's trailing whitespace dropped.
func ExtractOverrides(summary string) (string, Overrides) {
	var o Overrides
	if m := prOverrideRe.FindStringSubmatchIndex(summary); m != nil {
		if n, err := strconv.Atoi(summary[m[2]:m[3]]); err == nil {
			o.PR = n
		}
		summary = summary[:m[0]] + summary[m[1]:]
	}
	if m := commitOverrideRe.FindStringSubmatchIndex(summary); m != nil {
		o.Commit = summary[m[2]:m[3]]
		summary = summary[:m[0]] + summary[m[1]:]
	}
	summary = userOverrideRe.ReplaceAllStringFunc(summary, func(match string) string {
		o.Users = append(o.Users, userOverrideRe.FindStringSubmatch(match)[1])
		return ""
	})
	lines := strings.Split(strings.TrimSpace(summary), "\n")
	for i := range lines {
		lines[i] = strings.TrimRightFunc(lines[i], func(r rune) bool {
			return r == ' ' || r == '\t' || r == '\r'
		})
	}
	return strings.Join(lines, "\n"), o
}

// ApplyOverrides folds a summary's overrides into the commit info resolved
// for it (ok says whether there was any), as changelog-github does: a pr:
// takes the pull request's merge commit and author, a commit: (alone) that
// commit's pull request and author, and a commit: alongside a pr: replaces
// only the commit link. author: lines replace who is thanked. Lookups go
// through `gh api` and, as elsewhere, degrade to a missing field.
func ApplyOverrides(o Overrides, info CommitInfo, ok bool, repo, dir string, run Runner) (CommitInfo, bool) {
	switch {
	case o.PR != 0:
		info = CommitInfo{PullRequest: o.PR}
		if repo != "" {
			pr := strconv.Itoa(o.PR)
			fields := strings.Fields(runFirstLine(run, dir, "gh", "api", "repos/"+repo+"/pulls/"+pr,
				"--jq", `(if .merged_at then .merge_commit_sha else "-" end) + " " + (.user.login // "-")`))
			if len(fields) == 2 {
				if fields[0] != "-" {
					info.Commit, info.Short = fields[0], shortSHA(fields[0])
				}
				if fields[1] != "-" {
					info.Author = fields[1]
				}
			}
		}
		if o.Commit != "" {
			info.Commit, info.Short = fullCommit(run, dir, o.Commit), shortSHA(o.Commit)
		}
		ok = true
	case o.Commit != "":
		sha := fullCommit(run, dir, o.Commit)
		info = CommitInfo{Commit: sha, Short: shortSHA(o.Commit)}
		if repo != "" {
			info.PullRequest = pullRequestForCommit(run, dir, repo, sha)
			info.Author = authorForCommit(run, dir, repo, sha)
		}
		ok = true
	}
	if len(o.Users) > 0 {
		info.Users = o.Users
		ok = true
	}
	return info, ok
}

// fullCommit is the full SHA of a commit: override names, which are often
// abbreviated, so a generator is handed the same full SHA as elsewhere. The
// name as written when the repository doesn't have it.
func fullCommit(run Runner, dir, commit string) string {
	if sha := runFirstLine(run, dir, "git", "rev-parse", "--verify", "--quiet", commit+"^{commit}"); sha != "" {
		return sha
	}
	return commit
}
