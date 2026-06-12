// Ported from net-changesets Version/ChangelogReleaseLineTests.cs.
package changelog

import "testing"

func TestRenderLine(t *testing.T) {
	tests := []struct {
		name    string
		summary string
		setting Setting
		info    *CommitInfo
		want    string
	}{
		{
			name:    "default generator returns the summary unchanged",
			summary: "A change",
			setting: Setting{Kind: KindDefault},
			info:    &CommitInfo{Commit: "abc1234", PullRequest: 7, Author: "octocat"},
			want:    "A change",
		},
		{
			name:    "git prefixes the short commit",
			summary: "A change",
			setting: Setting{Kind: KindGit},
			info:    &CommitInfo{Commit: "abc1234"},
			want:    "abc1234: A change",
		},
		{
			name:    "git without a commit returns the summary unchanged",
			summary: "A change",
			setting: Setting{Kind: KindGit},
			info:    &CommitInfo{},
			want:    "A change",
		},
		{
			name:    "git only prefixes the first line",
			summary: "First line\n\nSecond paragraph",
			setting: Setting{Kind: KindGit},
			info:    &CommitInfo{Commit: "abc1234"},
			want:    "abc1234: First line\n\nSecond paragraph",
		},
		{
			name:    "github builds pr link commit link and thanks",
			summary: "A change",
			setting: Setting{Kind: KindGitHub, Repo: "acme/widgets"},
			info:    &CommitInfo{Commit: "abc1234", PullRequest: 42, Author: "octocat"},
			want:    "[#42](https://github.com/acme/widgets/pull/42) [`abc1234`](https://github.com/acme/widgets/commit/abc1234) Thanks [@octocat](https://github.com/octocat)! - A change",
		},
		{
			name:    "github without a pull request uses the commit link only",
			summary: "A change",
			setting: Setting{Kind: KindGitHub, Repo: "acme/widgets"},
			info:    &CommitInfo{Commit: "abc1234", Author: "octocat"},
			want:    "[`abc1234`](https://github.com/acme/widgets/commit/abc1234) Thanks [@octocat](https://github.com/octocat)! - A change",
		},
		{
			name:    "github without data returns the summary unchanged",
			summary: "A change",
			setting: Setting{Kind: KindGitHub, Repo: "acme/widgets"},
			info:    &CommitInfo{},
			want:    "A change",
		},
		// Unchanged cases from the C# implementation beyond its test suite.
		{
			name:    "github without commit info returns the summary unchanged",
			summary: "A change",
			setting: Setting{Kind: KindGitHub, Repo: "acme/widgets"},
			info:    nil,
			want:    "A change",
		},
		{
			name:    "github without a repo returns the summary unchanged",
			summary: "A change",
			setting: Setting{Kind: KindGitHub},
			info:    &CommitInfo{Commit: "abc1234", PullRequest: 42, Author: "octocat"},
			want:    "A change",
		},
		{
			name:    "github commit only still renders the thanks wrapper",
			summary: "A change",
			setting: Setting{Kind: KindGitHub, Repo: "acme/widgets"},
			info:    &CommitInfo{Commit: "abc1234"},
			want:    "[`abc1234`](https://github.com/acme/widgets/commit/abc1234) Thanks ! - A change",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RenderLine(tt.summary, tt.setting, tt.info); got != tt.want {
				t.Errorf("RenderLine() = %q, want %q", got, tt.want)
			}
		})
	}
}
