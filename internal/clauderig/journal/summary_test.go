package journal

import (
	"strings"
	"testing"
)

// Summary lives on the record so `clauderig status` and the UI's activity feed
// render one wording. These cases are the contract both depend on.
func TestSummary(t *testing.T) {
	tests := []struct {
		name  string
		rec   Record
		want  []string
		avoid []string
	}{
		{
			name: "sync counts",
			rec: Record{
				Op: OpSync, Outcome: OutcomeOK,
				Files: 1418, Redactions: 12, AgedOut: 3, Oversize: 2,
			},
			want: []string{"Synced 1418 files", "12 secrets redacted", "3 aged out", "2 files too large"},
		},
		{
			name:  "quiet sync stays short",
			rec:   Record{Op: OpSync, Outcome: OutcomeOK, Files: 4},
			want:  []string{"Synced 4 files"},
			avoid: []string{"redacted", "aged out", "too large"},
		},
		{
			name:  "singulars read naturally",
			rec:   Record{Op: OpSync, Outcome: OutcomeOK, Files: 1, Redactions: 1, Oversize: 1},
			want:  []string{"Synced 1 file,", "1 secret redacted", "1 file too large"},
			avoid: []string{"1 files", "1 secrets"},
		},
		{
			name:  "restore",
			rec:   Record{Op: OpRestore, Outcome: OutcomeOK, Files: 900},
			want:  []string{"Restored 900 files"},
			avoid: []string{"Synced"},
		},
		{
			name: "failure leads with the reason",
			rec: Record{
				Op: OpPull, Outcome: OutcomeFailed,
				Error: "fatal: Not possible to fast-forward, aborting.",
			},
			want: []string{"Pull failed:", "fast-forward"},
		},
		{
			name:  "failure without a reason still names the op",
			rec:   Record{Op: OpSync, Outcome: OutcomeFailed},
			want:  []string{"Sync failed"},
			avoid: []string{":"},
		},
		{
			// The tripwire is the safety property working. It must not read as
			// a crash, or people learn to scroll past the row that means a
			// secret nearly shipped.
			name: "refusal reads as a save, not a failure",
			rec: Record{
				Op: OpSync, Outcome: OutcomeRefused,
				Leaks: []Leak{{Path: "env.KEY", Kind: "anthropic-key"}, {Path: "mcp.token", Kind: "jwt"}},
			},
			want:  []string{"Refused to push", "2 values"},
			avoid: []string{"failed", "Failed"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.rec.Summary()
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

// A sync that wrote nothing must not read like a sync that did. Before the
// engine stopped counting regenerated-but-identical JSON as written, every run
// reported the same file and redaction totals, and the activity feed was an
// unbroken column of one identical line.
func TestQuietSyncSaysSo(t *testing.T) {
	rec := Record{
		Op:         OpSync,
		Outcome:    OutcomeOK,
		Unchanged:  1035,
		Redactions: 21,
	}
	got := rec.Summary()
	if want := "No changes — 1035 files already current"; got != want {
		t.Fatalf("Summary() = %q, want %q", got, want)
	}
	if strings.Contains(got, "redacted") {
		t.Errorf("a run that wrote nothing reported redactions: %q", got)
	}
}

// Pruning and size refusals happen to the staged copy whether or not anything
// new was written, so a quiet run still has to report them.
// Files past the window are re-counted every run because clauderig never
// deletes from ~/.claude. Summed into AgedOut they made every row report the
// same number forever and buried the runs that pruned something for real.
func TestStandingTooOldCountIsNotReportedAsAnEvent(t *testing.T) {
	rec := Record{
		Op:        OpSync,
		Outcome:   OutcomeOK,
		Unchanged: 3260,
		TooOld:    6,
	}
	got := rec.Summary()
	if strings.Contains(got, "aged out") {
		t.Errorf("a standing skip was reported as a prune: %q", got)
	}
	if want := "No changes — 3260 files already current"; got != want {
		t.Fatalf("Summary() = %q, want %q", got, want)
	}
}

func TestQuietSyncStillReportsPruning(t *testing.T) {
	rec := Record{
		Op:        OpSync,
		Outcome:   OutcomeOK,
		Unchanged: 10,
		AgedOut:   6,
		Oversize:  2,
	}
	got := rec.Summary()
	if want := "No changes — 10 files already current, 6 aged out, 2 files too large"; got != want {
		t.Fatalf("Summary() = %q, want %q", got, want)
	}
}

func TestBusySyncStillCountsWhatItWrote(t *testing.T) {
	rec := Record{
		Op:         OpSync,
		Outcome:    OutcomeOK,
		Files:      3,
		Unchanged:  1032,
		Redactions: 21,
	}
	if want, got := "Synced 3 files, 21 secrets redacted", rec.Summary(); got != want {
		t.Fatalf("Summary() = %q, want %q", got, want)
	}
}
