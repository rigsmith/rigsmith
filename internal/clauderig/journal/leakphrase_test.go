package journal

import "testing"

// The bug this pins: a plural noun helper with the verb written out beside it,
// which read correctly for every number except the commonest one.
//
//	1 value look like credentials      ← what status said
//	1 file(s) are credential material  ← what the journal recorded, same event
func TestLeakPhraseAgreesWithItsOwnCount(t *testing.T) {
	for _, tc := range []struct {
		name        string
		total, file int
		want        string
	}{
		{"one value", 1, 0, "1 value looks like a credential"},
		{"several values", 4, 0, "4 values look like credentials"},
		{"one file", 1, 1, "1 file is credential material"},
		{"several files", 3, 3, "3 files are credential material"},
		{"one of each", 2, 1, "1 file of credential material and 1 value that looks like a credential"},
		{"some of each", 5, 2, "2 files of credential material and 3 values that look like credentials"},
	} {
		if got := LeakPhrase(tc.total, tc.file); got != tc.want {
			t.Errorf("%s: LeakPhrase(%d, %d) = %q, want %q", tc.name, tc.total, tc.file, got, tc.want)
		}
	}
}

// A record written before LeakFiles existed has no such field, and reads as
// zero. That renders it as values — which is how it was always rendered, and
// the only honest answer for a record that never recorded the distinction.
func TestLeakPhraseReadsOlderRecordsAsValues(t *testing.T) {
	r := Record{Outcome: OutcomeRefused, Leaks: []Leak{{Path: "a", Kind: "private-key"}}}
	if got, want := r.Summary(), "Refused to push — 1 value looks like a credential"; got != want {
		t.Errorf("Summary() = %q, want %q", got, want)
	}
}

// The whole point of carrying the count: the sentence names what was actually
// caught, because a file and a value send you to different places.
func TestSummaryNamesAFileAsAFile(t *testing.T) {
	r := Record{
		Outcome:   OutcomeRefused,
		Leaks:     []Leak{{Path: "cli/projects/x/tool-results/y.txt", Kind: "private-key"}},
		LeakFiles: 1,
	}
	if got, want := r.Summary(), "Refused to push — 1 file is credential material"; got != want {
		t.Errorf("Summary() = %q, want %q", got, want)
	}
}
