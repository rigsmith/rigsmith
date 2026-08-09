package commands

import (
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/internal/clauderig/journal"
)

// The incident this row exists to prevent: 65 commits behind must never render
// as anything reassuring, and behind-only must stay distinguishable from
// diverged — that split is what drives the amber-vs-red tray.
func TestDivergenceLine(t *testing.T) {
	tests := []struct {
		name  string
		div   gitrepo.Divergence
		want  []string
		avoid []string
	}{
		{
			name: "level",
			div:  gitrepo.Divergence{Ref: "origin/main", Tracked: true},
			want: []string{"up to date"},
		},
		{
			name:  "never fetched",
			div:   gitrepo.Divergence{Ref: "origin/main"},
			want:  []string{"unknown", "origin/main", "not fetched"},
			avoid: []string{"up to date"},
		},
		{
			name:  "behind only points at pull",
			div:   gitrepo.Divergence{Ref: "origin/main", Tracked: true, Behind: 65},
			want:  []string{"65 commits behind", "clauderig pull"},
			avoid: []string{"up to date", "diverged"},
		},
		{
			name:  "ahead only points at sync",
			div:   gitrepo.Divergence{Ref: "origin/main", Tracked: true, Ahead: 3},
			want:  []string{"3 commits ahead", "clauderig sync"},
			avoid: []string{"up to date", "diverged"},
		},
		{
			name:  "diverged and clean says so",
			div:   gitrepo.Divergence{Ref: "origin/main", Tracked: true, Ahead: 15, Behind: 65},
			want:  []string{"diverged", "15 commits ahead", "65 commits behind", "merges cleanly"},
			avoid: []string{"up to date"},
		},
		{
			name:  "diverged and conflicting warns",
			div:   gitrepo.Divergence{Ref: "origin/main", Tracked: true, Ahead: 15, Behind: 65, Conflict: true},
			want:  []string{"diverged", "would conflict"},
			avoid: []string{"merges cleanly", "up to date"},
		},
		{
			// A half-finished merge outranks the counts: nothing else is
			// actionable until it is resolved.
			name:  "mid-merge outranks everything",
			div:   gitrepo.Divergence{Ref: "origin/main", Tracked: true, Ahead: 1, Behind: 1, Merging: true},
			want:  []string{"unresolved merge"},
			avoid: []string{"diverged", "up to date"},
		},
		{
			name: "singular commit reads naturally",
			div:  gitrepo.Divergence{Ref: "origin/main", Tracked: true, Behind: 1},
			want: []string{"1 commit behind"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := stripANSI(divergenceLine(tc.div))
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("want %q in %q", w, got)
				}
			}
			for _, a := range tc.avoid {
				if strings.Contains(got, a) {
					t.Errorf("did not want %q in %q", a, got)
				}
			}
		})
	}
}

// The `last run` row is what gives the CLI the same visibility the tray has.
func TestLastRunLine(t *testing.T) {
	dir := t.TempDir()

	// Nothing recorded yet → no row at all, rather than an empty one.
	if got := lastRunLine(dir, "pro"); got != "" {
		t.Fatalf("want no row before anything is journalled, got %q", got)
	}

	if err := journal.Append(dir, journal.Record{
		Machine: "pro", Op: journal.OpSync, Outcome: journal.OutcomeOK, Files: 1418, Redactions: 12,
	}); err != nil {
		t.Fatal(err)
	}
	got := stripANSI(lastRunLine(dir, "pro"))
	if !strings.Contains(got, "Synced 1418 files") || !strings.Contains(got, "12 secrets redacted") {
		t.Errorf("summary missing from row: %q", got)
	}
	// This machine's own run doesn't repeat the hostname.
	if strings.Contains(got, "pro") {
		t.Errorf("row named this machine unnecessarily: %q", got)
	}

	// A newer record from another machine wins, and is attributed.
	if err := journal.Append(dir, journal.Record{
		Machine: "air", Op: journal.OpPull, Outcome: journal.OutcomeFailed,
		Error: "fatal: Not possible to fast-forward, aborting.",
	}); err != nil {
		t.Fatal(err)
	}
	got = stripANSI(lastRunLine(dir, "pro"))
	if !strings.Contains(got, "Pull failed") || !strings.Contains(got, "fast-forward") {
		t.Errorf("want the newest record's failure, got %q", got)
	}
	if !strings.Contains(got, "air") {
		t.Errorf("another machine's run should be attributed: %q", got)
	}
}

// The status row is a summary, so a rambling git error gets clipped there — the
// full text still lives in the journal and in the UI's feed, which wraps.
func TestLastRunLineClipsLongErrors(t *testing.T) {
	dir := t.TempDir()
	if err := journal.Append(dir, journal.Record{
		Machine: "pro", Op: journal.OpSync, Outcome: journal.OutcomeFailed,
		Error: strings.Repeat("verbose git boilerplate ", 40),
	}); err != nil {
		t.Fatal(err)
	}

	got := stripANSI(lastRunLine(dir, "pro"))
	if !strings.Contains(got, "…") {
		t.Errorf("long error was not clipped: %q", got)
	}
	if n := len([]rune(got)); n > lastRunMaxWidth+24 { // + the "(when)" suffix
		t.Errorf("clipped row is still %d runes: %q", n, got)
	}
	// The meaning survives the cut.
	if !strings.HasPrefix(got, "Sync failed:") {
		t.Errorf("clip ate the beginning: %q", got)
	}
}

func TestClip(t *testing.T) {
	if got := clip("short enough", 40); got != "short enough" {
		t.Errorf("clip shortened a fitting string: %q", got)
	}
	// Cuts on a word boundary rather than mid-word.
	got := clip("alpha beta gamma delta epsilon", 20)
	if strings.Contains(got, "epsilon") || !strings.HasSuffix(got, "…") {
		t.Errorf("unexpected clip: %q", got)
	}
	if strings.HasSuffix(strings.TrimSuffix(got, "…"), " ") {
		t.Errorf("clip left trailing space before the ellipsis: %q", got)
	}
	// A single long word with no boundary still gets cut to length.
	if got := clip(strings.Repeat("x", 100), 10); len([]rune(got)) != 11 {
		t.Errorf("boundary-less clip = %q (%d runes)", got, len([]rune(got)))
	}
}
