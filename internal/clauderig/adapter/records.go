package adapter

import (
	"github.com/rigsmith/rigsmith/internal/agentrig/records"
	"github.com/rigsmith/rigsmith/internal/clauderig/ledger"
	"github.com/rigsmith/rigsmith/internal/clauderig/session"
)

// LedgerSummary exposes read-only common facts. Unknown native fields remain in
// ledger.Entry and never round-trip through the shared summary.
func LedgerSummary(e ledger.Entry) records.Summary {
	return records.Summary{Vendor: "claude", ID: e.ID, Project: e.Slug, Cwd: e.Cwd, Title: e.Title,
		Activity: e.End, Approximate: true, Bytes: e.Bytes, RecordedBy: e.RecordedBy, Seen: e.Seen,
		Account: e.Account, AccountSource: e.AccountSource, AccountSince: e.AccountSince}
}

// MetadataSummary exposes Desktop metadata without turning profiles into accounts.
func MetadataSummary(m session.Meta) records.Summary {
	return records.Summary{Vendor: "claude", ID: m.ID, Title: m.Title, Cwd: m.Cwd, Activity: m.LastActivity, Approximate: true, Profile: m.Profile, Account: m.Account, Sources: append([]string(nil), m.Sources...)}
}

// LedgerStore translates newly read summaries into the existing native writer.
// Ledger.Note retains unknown fields and Claude's sticky attribution semantics.
type LedgerStore struct{ *ledger.Ledger }

func (s LedgerStore) Note(e records.Summary) bool {
	return s.Ledger.Note(ledger.Entry{ID: e.ID, Slug: e.Project, Cwd: e.Cwd, Title: e.Title,
		End: e.Activity, Bytes: e.Bytes, RecordedBy: e.RecordedBy, Seen: e.Seen,
		Account: e.Account, AccountSource: e.AccountSource, AccountSince: e.AccountSince})
}
