// Ported from net-changesets Commands/Version/Helpers/ChangelogReleaseLine.cs.
package changelog

import "strconv"

// RenderLine applies the configured changelog generator to a changeset
// summary, mirroring @changesets' getReleaseLine. It only transforms the first
// line (prepending a commit hash, or PR/commit/author links); continuation
// lines and the bullet itself are added later by the changelog writer.
func RenderLine(summary string, setting Setting, info *CommitInfo) string {
	var prefix string
	switch setting.Kind {
	case KindGit:
		prefix = gitPrefix(info)
	case KindGitHub:
		prefix = gitHubPrefix(info, setting.Repo)
	}

	if prefix == "" {
		return summary
	}
	// The prefix lands on the first line; continuation lines are untouched.
	return prefix + summary
}

// gitPrefix renders @changesets/changelog-git: "<commit>: <summary>".
func gitPrefix(info *CommitInfo) string {
	if info == nil || info.Commit == "" {
		return ""
	}
	return info.Commit + ": "
}

// gitHubPrefix renders @changesets/changelog-github:
// "[#pr](url) [`commit`](url) Thanks [@user](url)! - <summary>".
// Each link is omitted when its datum is missing; with no info, no repo, or
// all three missing the summary is left unchanged.
func gitHubPrefix(info *CommitInfo, repo string) string {
	if info == nil || repo == "" {
		return ""
	}

	var pullLink, commitLink, userLink string
	if info.PullRequest != 0 {
		pr := strconv.Itoa(info.PullRequest)
		pullLink = "[#" + pr + "](https://github.com/" + repo + "/pull/" + pr + ")"
	}
	if info.Commit != "" {
		commitLink = "[`" + info.Commit + "`](https://github.com/" + repo + "/commit/" + info.Commit + ")"
	}
	if info.Author != "" {
		userLink = "[@" + info.Author + "](https://github.com/" + info.Author + ")"
	}

	if pullLink == "" && commitLink == "" && userLink == "" {
		return ""
	}

	var prefix string
	if pullLink != "" {
		prefix += pullLink + " "
	}
	if commitLink != "" {
		prefix += commitLink + " "
	}
	// As in the C# source, "Thanks" appears whenever any datum exists, even
	// when the user link itself is missing.
	prefix += "Thanks " + userLink + "! - "
	return prefix
}
