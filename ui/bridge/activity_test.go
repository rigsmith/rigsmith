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
