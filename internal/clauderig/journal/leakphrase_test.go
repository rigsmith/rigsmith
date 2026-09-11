package journal

import (
	"errors"
	"testing"

	"github.com/rigsmith/rigsmith/internal/clauderig/engine"
	"github.com/rigsmith/rigsmith/internal/clauderig/redact"
)

// The bug this pins: a plural noun helper with the verb written out beside it,
// which read correctly for every number except the commonest one.
//
//	1 value look like credentials      ← what status said
//	1 file(s) are credential material  ← what the journal recorded, same event
func TestLeakPhraseAgreesWithItsOwnCount(t *testing.T) {
	for _, tc := range []struct {
		name                string
		total, file, unread int
		want                string
	}{
		{"one value", 1, 0, 0, "1 value looks like a credential"},
		{"several values", 4, 0, 0, "4 values look like credentials"},
		{"one file", 1, 1, 0, "1 file is credential material"},
		{"several files", 3, 3, 0, "3 files are credential material"},
		{"one of each", 2, 1, 0, "1 file of credential material and 1 value that looks like a credential"},
		{"some of each", 5, 2, 0, "2 files of credential material and 3 values that look like credentials"},
		// Unreadable is neither of the other two, and saying so is the whole
		// point: a file nobody could open has not been shown to hold anything.
		{"one unreadable", 1, 0, 1, "1 file could not be read"},
		{"unreadable among files", 3, 2, 1, "2 files of credential material and 1 file that could not be read"},
		{"all three", 4, 1, 1, "1 file of credential material, 2 values that look like credentials and 1 file that could not be read"},
	} {
		if got := LeakPhrase(tc.total, tc.file, tc.unread); got != tc.want {
			t.Errorf("%s: LeakPhrase(%d, %d, %d) = %q, want %q",
				tc.name, tc.total, tc.file, tc.unread, got, tc.want)
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

// The count is DERIVED from the findings, so the two ways a whole-file finding
// can appear — during the walk, and from the post-copy audit — both land in it.
// The version this replaces counted during the walk only: an audit-only finding
// was recorded as a value, and a report that returned before the audit carried
// a count of zero beside a list of findings.
func TestFromSyncCountsFileFindingsWhereverTheyCameFrom(t *testing.T) {
	rec := FromSync("mbp", &engine.Report{Findings: []redact.Finding{
		{Path: "cli/a/id_rsa", Kind: "private-key", File: true},
		{Path: "cli/b.json:token", Kind: "anthropic-key"},
		{Path: "cli/c/.audit-key", Kind: "key-material", File: true},
	}}, errors.New("secret tripwire: refusing to sync"))
	if rec.LeakFiles != 2 {
		t.Errorf("LeakFiles = %d, want both whole-file findings", rec.LeakFiles)
	}
	if len(rec.Leaks) != 3 {
		t.Errorf("Leaks = %d, want every finding", len(rec.Leaks))
	}
	if got, want := rec.Summary(),
		"Refused to push — 2 files of credential material and 1 value that looks like a credential"; got != want {
		t.Errorf("Summary() = %q, want %q", got, want)
	}
}

// The two halves of the tripwire are not the same thing, and the compatibility
// harness is what proved it: a github token inside a transcript came out as
// "2 files are credential material" when the baseline had always called it what
// it is. A file is credential material when its NAME says so, or when it
// carries a PEM block that cannot be redacted out of a non-JSON file. A token
// sitting in someone's conversation is a value the redactor can scrub.
func TestFileFindingsAreFilesAndEmbeddedTokensAreValues(t *testing.T) {
	rec := FromSync("mbp", &engine.Report{Findings: []redact.Finding{
		{Path: "cli/projects/x/s.jsonl", Kind: "github-token"},          // in a transcript
		{Path: "cli/projects/x/s.jsonl", Kind: "jwt"},                   // likewise
		{Path: "cli/skills/s/id_rsa", Kind: "key-material", File: true}, // the file itself
	}}, errors.New("secret tripwire"))

	if rec.LeakFiles != 1 {
		t.Errorf("LeakFiles = %d, want only the key file", rec.LeakFiles)
	}
	if got, want := rec.Summary(),
		"Refused to push — 1 file of credential material and 2 values that look like credentials"; got != want {
		t.Errorf("Summary() = %q, want %q", got, want)
	}
}

// The count says how many findings were whole files; this says WHICH. Kind
// cannot answer it — "private-key" is what both a PEM block inside a transcript
// and an id_rsa report — and the two send you to different places, which is the
// whole reason the distinction is drawn.
func TestLeakLabelSeparatesAFileFromAValueOfTheSameKind(t *testing.T) {
	value := Leak{Path: "cli/projects/x/s.jsonl", Kind: "private-key"}
	file := Leak{Path: "cli/skills/s/id_rsa", Kind: "private-key", File: true}

	if got, want := value.Label(), "cli/projects/x/s.jsonl (private-key)"; got != want {
		t.Errorf("value = %q, want %q", got, want)
	}
	if got, want := file.Label(), "cli/skills/s/id_rsa (private-key file)"; got != want {
		t.Errorf("file = %q, want %q", got, want)
	}
	if value.Label() == file.Label() {
		t.Error("a value and a whole file of the same kind are indistinguishable")
	}
}

// An unreadable file carries File as well, but its kind already says so. Saying
// it twice — "unreadable file" — is the kind of wording this PR exists to stop.
func TestLeakLabelDoesNotSayUnreadableTwice(t *testing.T) {
	l := Leak{Path: "cli/projects/x/s.jsonl", Kind: redact.KindUnreadable, File: true}
	if got, want := l.Label(), "cli/projects/x/s.jsonl (unreadable)"; got != want {
		t.Errorf("label = %q, want %q", got, want)
	}
}

// A record written before the distinction existed has no File on any finding.
// It reads as a value, which is how it has always been rendered.
func TestLeakLabelOnAnOlderRecordReadsAsAValue(t *testing.T) {
	old := Leak{Path: "env.KEY", Kind: "anthropic-key"}
	if got, want := old.Label(), "env.KEY (anthropic-key)"; got != want {
		t.Errorf("label = %q, want %q", got, want)
	}
}
