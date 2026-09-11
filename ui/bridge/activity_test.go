package bridge

import (
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/clauderig/journal"
)

// Leaks reach the UI as readable "path (kind)" strings so a refusal can show
// what it caught, and rows are tagged as this machine's or another's.
func TestToEvent(t *testing.T) {
	at := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	rec := journal.Record{
		At: at, Machine: "air", Op: journal.OpSync, Outcome: journal.OutcomeRefused,
		Leaks: []journal.Leak{{Path: "env.KEY", Kind: "anthropic-key"}},
	}

	ev := toEvent(rec, "air")
	if !ev.This {
		t.Error("record from this machine not flagged")
	}
	if ev.Op != "sync" || ev.Outcome != "refused" || !ev.At.Equal(at) {
		t.Errorf("unexpected event: %+v", ev)
	}
	if len(ev.Leaks) != 1 || ev.Leaks[0] != "env.KEY (anthropic-key)" {
		t.Errorf("leaks = %v", ev.Leaks)
	}

	if other := toEvent(rec, "pro"); other.This {
		t.Error("another machine's record flagged as this one")
	}
}

// The window's activity feed and `clauderig status` describe one record, and
// the whole point of rendering through Record.Summary is that they cannot say
// different things about it. Only a test holds that: toEvent could start
// composing its own sentence and nothing else would notice.
//
// Compared against the record's OWN summary rather than against a sentence
// written out here. What the feed must do is forward; what the sentence must
// say is pinned where it is produced, in journal and health. Restating it here
// would mean a wording change breaks three files and this test stops being
// about forwarding at all.
func TestActivityForwardsTheRecordsOwnSummary(t *testing.T) {
	for _, tc := range []struct {
		name string
		rec  journal.Record
	}{
		{"a whole file", journal.Record{
			Op: journal.OpSync, Outcome: journal.OutcomeRefused,
			Leaks: []journal.Leak{{Path: "cli/skills/s/id_rsa", Kind: "key-material", File: true}}, LeakFiles: 1,
		}},
		{"a file and a value", journal.Record{
			Op: journal.OpSync, Outcome: journal.OutcomeRefused,
			Leaks:     []journal.Leak{{Path: "a", Kind: "key-material", File: true}, {Path: "b", Kind: "github-token"}},
			LeakFiles: 1,
		}},
		{"one that could not be read", journal.Record{
			Op: journal.OpSync, Outcome: journal.OutcomeRefused,
			Leaks:      []journal.Leak{{Path: "a", Kind: "unreadable", File: true}},
			LeakUnread: 1,
		}},
		{"an ordinary sync", journal.Record{
			Op: journal.OpSync, Outcome: journal.OutcomeOK, Files: 3,
		}},
	} {
		e := toEvent(tc.rec, "mbp")
		if want := tc.rec.Summary(); e.Summary != want {
			t.Errorf("%s: Summary = %q, want the record's own %q", tc.name, e.Summary, want)
		}
		if e.Summary == "" {
			t.Errorf("%s: the feed would show a blank line", tc.name)
		}
		if len(e.Leaks) != len(tc.rec.Leaks) {
			t.Errorf("%s: %d leak lines, want one per finding", tc.name, len(e.Leaks))
		}
	}
}

// The feed shows one line per finding, and those lines have to carry the same
// distinction the summary above them counts — otherwise a refusal reads
// "1 file" over two rows that look identical.
func TestActivityDistinguishesAWholeFileFromAValue(t *testing.T) {
	e := toEvent(journal.Record{
		Op: journal.OpSync, Outcome: journal.OutcomeRefused,
		Leaks: []journal.Leak{
			{Path: "cli/projects/x/s.jsonl", Kind: "private-key"},
			{Path: "cli/skills/s/id_rsa", Kind: "private-key", File: true},
		},
		LeakFiles: 1,
	}, "mbp")

	if len(e.Leaks) != 2 {
		t.Fatalf("leaks = %v", e.Leaks)
	}
	if e.Leaks[0] == e.Leaks[1] {
		t.Errorf("both rows read %q — the file and the value are indistinguishable", e.Leaks[0])
	}
	if e.Leaks[1] != "cli/skills/s/id_rsa (private-key file)" {
		t.Errorf("whole-file row = %q", e.Leaks[1])
	}
}
