package commands

import (
	"bytes"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/internal/clauderig/status"
)

func TestPrintAccountLine(t *testing.T) {
	cases := []struct {
		name string
		in   status.AccountInfo
		want []string
		deny []string
	}{
		{
			name: "the ordinary case is one short line",
			in:   status.AccountInfo{Email: "john@work.com", Subscription: "max", Desynced: false},
			want: []string{"john@work.com", "max"},
			deny: []string{"desync", "not tracked", "pointer"},
		},
		{
			name: "an alias is shown because it is what the user types",
			in:   status.AccountInfo{Email: "john@work.com", Alias: "dev", Desynced: false},
			want: []string{"john@work.com", "dev"},
		},
		{
			// The failure this whole command family exists to catch: requests
			// authenticate as one account while Claude Code displays another.
			name: "a desync is called out, not merely omitted",
			in:   status.AccountInfo{Email: "john@work.com", Desynced: true},
			want: []string{"desynced", "account doctor"},
		},
		{
			name: "a login clauderig has never captured says so",
			in:   status.AccountInfo{Email: "john@new.com", Desynced: false, Untracked: true},
			want: []string{"not tracked", "account add"},
		},
		{
			// `account list`'s arrow would be naming a different account.
			name: "a stale pointer is surfaced",
			in:   status.AccountInfo{Email: "john@work.com", PointerEmail: "john@home.com"},
			want: []string{"pointer", "john@home.com"},
			// Pointer drift is NOT a desync: the two identity halves can agree
			// perfectly while clauderig's own pointer lags.
			deny: []string{"desynced"},
		},
		{
			name: "a logout is an ordinary state, not a problem",
			in:   status.AccountInfo{LoggedOut: true},
			want: []string{"not logged in"},
			deny: []string{"could not"},
		},
		{
			name: "an unreadable identity reports the problem rather than a blank",
			in:   status.AccountInfo{Problem: "the live credential could not be read"},
			want: []string{"could not be read"},
			deny: []string{"not logged in"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			printAccountLine(&buf, tc.in)
			got := buf.String()
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("output should mention %q:\n%s", w, got)
				}
			}
			for _, d := range tc.deny {
				if strings.Contains(got, d) {
					t.Errorf("output should not mention %q:\n%s", d, got)
				}
			}
		})
	}
}
