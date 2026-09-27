// Ported from net-changesets Commands/Version/Helpers/ChangelogReleaseLine.cs.
package changelog

import (
	"regexp"
	"strconv"
	"strings"
)

// RenderLine applies the configured changelog generator to a changeset
// summary, mirroring @changesets' getReleaseLine. It transforms the first
// line (prepending a commit hash, or PR/commit/author links, or rendering
// changelog-github's `template`); continuation lines and the bullet itself
// are added later by the changelog writer. changelog-github also links each
// bare `#123` in the summary to its issue, on every line.
func RenderLine(summary string, setting Setting, info *CommitInfo) string {
	switch setting.Kind {
	case KindGit:
		return gitPrefix(info) + summary
	case KindGitHub:
		return renderGitHub(summary, setting, info)
	}
	return summary
}

// gitPrefix renders @changesets/changelog-git: "<commit>: <summary>", the
// commit in its display form (7 characters, more where 7 is ambiguous).
func gitPrefix(info *CommitInfo) string {
	if info == nil || info.Commit == "" {
		return ""
	}
	return info.Display() + ": "
}

// renderGitHub renders @changesets/changelog-github:
// "[#pr](url) [`commit`](url) Thanks [@user](url)! - <summary>". As there,
// the commit link's URL carries the full SHA and its text the short form.
// Each link is omitted when its datum is missing, and with none of them the
// summary stands alone; with no repo it's left unchanged. No line says
// "Thanks" with `disableThanks`, and an author the `contributors` config
// excludes (a bot, or a maintainer who'd otherwise thank themselves on every
// line) gets none either; the links stay. With a `template`, the template
// renders the first line instead.
func renderGitHub(summary string, setting Setting, info *CommitInfo) string {
	repo := setting.Repo
	if repo == "" {
		return summary
	}
	lines := strings.Split(summary, "\n")
	for i := range lines {
		lines[i] = linkIssueRefs(lines[i], repo)
	}
	first, rest := lines[0], ""
	if len(lines) > 1 {
		rest = "\n" + strings.Join(lines[1:], "\n")
	}

	var pullLink, commitLink, users string
	if info != nil {
		if info.PullRequest != 0 {
			pr := strconv.Itoa(info.PullRequest)
			pullLink = "[#" + pr + "](https://github.com/" + repo + "/pull/" + pr + ")"
		}
		if info.Commit != "" {
			commitLink = "[`" + info.Display() + "`](https://github.com/" + repo + "/commit/" + info.Commit + ")"
		}
		if !setting.DisableThanks {
			users = thanked(info, setting)
		}
	}

	if setting.Template != "" {
		ref := ""
		if pullLink != "" {
			ref = "(" + pullLink + ")"
		} else if commitLink != "" {
			ref = "(" + commitLink + ")"
		}
		tokens := map[string]string{"summary": first, "ref": ref, "pull": pullLink, "commit": commitLink, "authors": users}
		line := templateTokenRe.ReplaceAllStringFunc(setting.Template, func(m string) string {
			return tokens[m[1:len(m)-1]]
		})
		// A canon template writes its own "\n\n- " bullet; the changelog
		// writer adds the bullet (and any scope) here, so it's dropped.
		line = strings.TrimLeft(line, " \t\r\n")
		line = strings.TrimPrefix(line, "- ")
		return strings.TrimRight(line, " \t\r\n") + rest
	}

	var prefix string
	if pullLink != "" {
		prefix += pullLink + " "
	}
	if commitLink != "" {
		prefix += commitLink + " "
	}
	// As @changesets/changelog-github does: "Thanks …!" only when there's a
	// user to thank. (The C# port wrote it whenever any link existed, which
	// left "Thanks !" when the author couldn't be resolved.)
	if users != "" {
		prefix += "Thanks " + users + "! "
	}
	if prefix == "" {
		return first + rest
	}
	return prefix + "- " + first + rest
}

// thanked links the people a line thanks: those the summary names with
// `author:` lines, else the commit's author, leaving out anyone the
// `contributors` config excludes.
func thanked(info *CommitInfo, setting Setting) string {
	users := info.Users
	if len(users) == 0 && info.Author != "" {
		users = []string{info.Author}
	}
	var links []string
	for _, u := range users {
		if !setting.Contributors.IsContributorExcluded(u, "", "") {
			links = append(links, "[@"+u+"](https://github.com/"+u+")")
		}
	}
	return strings.Join(links, ", ")
}

// issueRefRe matches an existing Markdown link (left alone) or a bare #123,
// as changelog-github's ISSUE_REF_REGEX does.
var issueRefRe = regexp.MustCompile(`\[.*?\]\(.*?\)|\B#([1-9]\d*)\b`)

// linkIssueRefs links each bare #123 on a line to the repository's issue.
func linkIssueRefs(line, repo string) string {
	return issueRefRe.ReplaceAllStringFunc(line, func(m string) string {
		if strings.HasPrefix(m, "[") {
			return m
		}
		return "[" + m + "](https://github.com/" + repo + "/issues/" + m[1:] + ")"
	})
}
