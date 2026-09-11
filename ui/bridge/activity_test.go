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
func TestActivityForwardsTheRecordsOwnSummary(t *testing.T) {
	for _, tc := range []struct {
		name string
		rec  journal.Record
		want string
	}{
		{
			"a whole file",
			journal.Record{
				Op: journal.OpSync, Outcome: journal.OutcomeRefused,
				Leaks: []journal.Leak{{Path: "cli/skills/s/id_rsa", Kind: "key-material"}}, LeakFiles: 1,
			},
			"Refused to push — 1 file is credential material",
		},
		{
			"a file and a value",
			journal.Record{
				Op: journal.OpSync, Outcome: journal.OutcomeRefused,
				Leaks:     []journal.Leak{{Path: "a", Kind: "key-material"}, {Path: "b", Kind: "github-token"}},
				LeakFiles: 1,
			},
			"Refused to push — 1 file of credential material and 1 value that looks like a credential",
		},
		{
			"one that could not be read",
			journal.Record{
				Op: journal.OpSync, Outcome: journal.OutcomeRefused,
				Leaks:      []journal.Leak{{Path: "a", Kind: "unreadable"}},
				LeakUnread: 1,
			},
			"Refused to push — 1 file could not be read",
		},
	} {
		e := toEvent(tc.rec, "mbp")
		if e.Summary != tc.want {
			t.Errorf("%s: Summary = %q, want %q", tc.name, e.Summary, tc.want)
		}
		if len(e.Leaks) != len(tc.rec.Leaks) {
			t.Errorf("%s: %d leak lines, want one per finding", tc.name, len(e.Leaks))
		}
	}
}
