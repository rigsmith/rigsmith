package changelog

import (
	"reflect"
	"strings"
	"testing"
)

func TestExtractOverrides(t *testing.T) {
	tests := []struct {
		name    string
		summary string
		want    string
		o       Overrides
	}{
		{
			name:    "no overrides leaves the summary",
			summary: "A change\n\nMore detail",
			want:    "A change\n\nMore detail",
		},
		{
			name:    "pr, commit and authors come out of the summary",
			summary: "pr: #12\ncommit: abc1234\nauthor: @octocat\nauthor: hubot\nA change",
			want:    "A change",
			o:       Overrides{PR: 12, Commit: "abc1234", Users: []string{"octocat", "hubot"}},
		},
		{
			name:    "pull request spelled out, any case",
			summary: "A change\nPull Request: 7",
			want:    "A change",
			o:       Overrides{PR: 7},
		},
		{
			// As changelog-github: only the first pr: counts, and a later
			// one stays in the text.
			name:    "only the first pr counts",
			summary: "pr: 1\npr: 2\nA change",
			want:    "pr: 2\nA change",
			o:       Overrides{PR: 1},
		},
		{
			// Only the matched part goes; the rest of its line stays.
			name:    "text after an override on its line stays",
			summary: "user: @octocat thanks!\nA change",
			want:    "thanks!\nA change",
			o:       Overrides{Users: []string{"octocat"}},
		},
		{
			name:    "an override mid-line isn't one",
			summary: "A change for pr: 12",
			want:    "A change for pr: 12",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, o := ExtractOverrides(tt.summary)
			if got != tt.want || !reflect.DeepEqual(o, tt.o) {
				t.Errorf("ExtractOverrides() = %q, %+v; want %q, %+v", got, o, tt.want, tt.o)
			}
		})
	}
}

// ghStub answers the gh api calls ApplyOverrides makes, recording each path.
func ghStub(calls *[]string, answers map[string]string) Runner {
	return func(dir, name string, args ...string) (string, error) {
		*calls = append(*calls, args[1])
		return answers[args[1]], nil
	}
}

func TestApplyOverrides(t *testing.T) {
	base := CommitInfo{Commit: full, Short: "abc1234", PullRequest: 3, Author: "someone"}

	t.Run("pr takes the merge commit and the pull request's author", func(t *testing.T) {
		var calls []string
		run := ghStub(&calls, map[string]string{"repos/acme/widgets/pulls/12": "fedcba9876543210 octocat"})
		got, ok := ApplyOverrides(Overrides{PR: 12}, base, true, "acme/widgets", "", run)
		want := CommitInfo{Commit: "fedcba9876543210", Short: "fedcba9", PullRequest: 12, Author: "octocat"}
		if !ok || !reflect.DeepEqual(got, want) {
			t.Errorf("got %+v, %v; want %+v", got, ok, want)
		}
	})

	t.Run("an unmerged pr has no commit", func(t *testing.T) {
		var calls []string
		run := ghStub(&calls, map[string]string{"repos/acme/widgets/pulls/12": "- octocat"})
		got, _ := ApplyOverrides(Overrides{PR: 12}, base, true, "acme/widgets", "", run)
		if got.Commit != "" || got.Author != "octocat" {
			t.Errorf("got %+v", got)
		}
	})

	t.Run("commit beside a pr replaces only the commit", func(t *testing.T) {
		var calls []string
		run := ghStub(&calls, map[string]string{"repos/acme/widgets/pulls/12": "fedcba9876543210 octocat"})
		got, _ := ApplyOverrides(Overrides{PR: 12, Commit: "1234567abc"}, base, true, "acme/widgets", "", run)
		want := CommitInfo{Commit: "1234567abc", Short: "1234567", PullRequest: 12, Author: "octocat"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %+v; want %+v", got, want)
		}
	})

	t.Run("commit alone looks up its pull request and author", func(t *testing.T) {
		var calls []string
		run := ghStub(&calls, map[string]string{
			"repos/acme/widgets/commits/1234567abc/pulls": "9",
			"repos/acme/widgets/commits/1234567abc":       "hubot",
		})
		got, ok := ApplyOverrides(Overrides{Commit: "1234567abc"}, CommitInfo{}, false, "acme/widgets", "", run)
		want := CommitInfo{Commit: "1234567abc", Short: "1234567", PullRequest: 9, Author: "hubot"}
		if !ok || !reflect.DeepEqual(got, want) {
			t.Errorf("got %+v, %v; want %+v", got, ok, want)
		}
	})

	t.Run("authors alone keep the resolved links", func(t *testing.T) {
		var calls []string
		got, ok := ApplyOverrides(Overrides{Users: []string{"a", "b"}}, base, true, "acme/widgets", "", ghStub(&calls, nil))
		want := base
		want.Users = []string{"a", "b"}
		if !ok || !reflect.DeepEqual(got, want) || len(calls) != 0 {
			t.Errorf("got %+v, %v, calls %v; want %+v and no calls", got, ok, calls, want)
		}
	})

	t.Run("no overrides changes nothing", func(t *testing.T) {
		var calls []string
		got, ok := ApplyOverrides(Overrides{}, CommitInfo{}, false, "acme/widgets", "", ghStub(&calls, nil))
		if ok || !reflect.DeepEqual(got, CommitInfo{}) || len(calls) != 0 {
			t.Errorf("got %+v, %v, calls %s", got, ok, strings.Join(calls, " "))
		}
	})
}
