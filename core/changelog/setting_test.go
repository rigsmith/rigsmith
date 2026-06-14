// Exercises the ChangelogSetting mapping ported from net-changesets
// Shared/ChangelogSetting.cs (kind mapping plus tuple repo extraction).
package changelog

import (
	"testing"

	"github.com/rigsmith/rigsmith/core/config"
)

func TestParseSetting(t *testing.T) {
	tests := []struct {
		name string
		json string // full config.json body
		want Setting
	}{
		{
			name: "absent maps to default",
			json: `{}`,
			want: Setting{Kind: KindDefault},
		},
		{
			name: "false maps to default",
			json: `{"changelog": false}`,
			want: Setting{Kind: KindDefault},
		},
		{
			name: "null maps to default",
			json: `{"changelog": null}`,
			want: Setting{Kind: KindDefault},
		},
		{
			name: "stock cli changelog string maps to default",
			json: `{"changelog": "@changesets/cli/changelog"}`,
			want: Setting{Kind: KindDefault},
		},
		{
			name: "unrecognized string maps to default",
			json: `{"changelog": "./my-changelog.js"}`,
			want: Setting{Kind: KindDefault},
		},
		{
			name: "changelog-git string maps to git",
			json: `{"changelog": "@changesets/changelog-git"}`,
			want: Setting{Kind: KindGit},
		},
		{
			name: "changelog-github string without options has no repo",
			json: `{"changelog": "@changesets/changelog-github"}`,
			want: Setting{Kind: KindGitHub},
		},
		{
			name: "github tuple extracts the repo",
			json: `{"changelog": ["@changesets/changelog-github", {"repo": "acme/widgets"}]}`,
			want: Setting{Kind: KindGitHub, Repo: "acme/widgets"},
		},
		{
			name: "github tuple without options leaves the repo empty",
			json: `{"changelog": ["@changesets/changelog-github"]}`,
			want: Setting{Kind: KindGitHub},
		},
		{
			name: "github tuple with empty options leaves the repo empty",
			json: `{"changelog": ["@changesets/changelog-github", {}]}`,
			want: Setting{Kind: KindGitHub},
		},
		{
			name: "github tuple with empty repo keeps it empty",
			json: `{"changelog": ["@changesets/changelog-github", {"repo": ""}]}`,
			want: Setting{Kind: KindGitHub},
		},
		{
			name: "github tuple with a non-string repo ignores it",
			json: `{"changelog": ["@changesets/changelog-github", {"repo": 5}]}`,
			want: Setting{Kind: KindGitHub},
		},
		{
			// The C# converter pulls the repo from the options object for any
			// generator, not only changelog-github.
			name: "git tuple still extracts the repo",
			json: `{"changelog": ["@changesets/changelog-git", {"repo": "acme/widgets"}]}`,
			want: Setting{Kind: KindGit, Repo: "acme/widgets"},
		},
		{
			name: "tuple with an unrecognized name maps to default",
			json: `{"changelog": [42, {"repo": "acme/widgets"}]}`,
			want: Setting{Kind: KindDefault, Repo: "acme/widgets"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := config.Parse([]byte(tt.json))
			if err != nil {
				t.Fatal(err)
			}
			if got := ParseSetting(cfg); got != tt.want {
				t.Errorf("ParseSetting() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
