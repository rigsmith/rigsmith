package bridge

import (
	"context"
	"strings"
	"time"

	"github.com/rigsmith/rigsmith/core/gitrepo"

	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/journal"
)

// DefaultActivityLimit is how many rows the window asks for. Enough to cover
// several days of hook-driven syncs without handing the frontend the whole
// bounded file.
const DefaultActivityLimit = 50

// Event is one row of the activity feed.
type Event struct {
	At      time.Time `json:"at"`
	Machine string    `json:"machine"`
	Op      string    `json:"op"`      // sync | pull | restore
	Outcome string    `json:"outcome"` // ok | failed | refused
	// Summary is the row's one-line human text, from journal.Record.Summary —
	// the same string `clauderig status` prints, so the two surfaces can never
	// describe one record differently.
	Summary string `json:"summary"`
	Error   string `json:"error,omitempty"`
	// Leaks names the tripwire findings, so a refusal can show what it caught
	// rather than just that it caught something.
	Leaks []string `json:"leaks,omitempty"`
	// Redacted names the files this run cleaned, so the expanded file list can
	// mark them. "21 secrets redacted" was never actionable without the where.
	Redacted []journal.RedactedFile `json:"redacted,omitempty"`
	This     bool                   `json:"this"`
}

// Activity is the service backing the window's feed.
type Activity struct{}

// NewActivity builds the activity service.
func NewActivity() *Activity { return &Activity{} }

// Recent returns the newest events across every machine, newest first. limit <= 0
// falls back to DefaultActivityLimit.
//
// Local read of the staging dir, so — like the status poll — it never blocks on
// the network.
func (a *Activity) Recent(ctx context.Context, limit int) ([]Event, error) {
	if limit <= 0 {
		limit = DefaultActivityLimit
	}
	cfg, err := config.LoadOrDefault()
	if err != nil {
		return nil, err
	}
	staging, err := config.StagingDir()
	if err != nil {
		return nil, err
	}
	recs, err := journal.Read(staging, limit)
	if err != nil {
		return nil, err
	}

	me := config.DetectFor(cfg)
	events := make([]Event, 0, len(recs))
	for _, r := range recs {
		events = append(events, toEvent(r, me.Name))
	}
	return events, nil
}

func toEvent(r journal.Record, thisMachine string) Event {
	e := Event{
		At:       r.At,
		Machine:  r.Machine,
		Op:       string(r.Op),
		Outcome:  string(r.Outcome),
		Summary:  r.Summary(),
		Error:    r.Error,
		Redacted: r.RedactedFiles,
		This:     r.Machine == thisMachine,
	}
	for _, l := range r.Leaks {
		e.Leaks = append(e.Leaks, l.Path+" ("+l.Kind+")")
	}
	return e
}

// MaxEventFiles bounds one expansion. A sync that rewrote a whole tree — the
// first one on a machine, or the one after a squash — would otherwise hand the
// window a thousand rows nobody reads.
const MaxEventFiles = 200

// EventFiles is what one recorded operation actually touched.
type EventFiles struct {
	// Commit is the short sha, so a curious reader can go and look themselves.
	Commit string               `json:"commit,omitempty"`
	Files  []gitrepo.FileChange `json:"files,omitempty"`
	// Truncated is how many paths were dropped to stay under MaxEventFiles. A
	// silent cap would read as "that was everything".
	Truncated int    `json:"truncated,omitempty"`
	Note      string `json:"note,omitempty"`
}

// Files lists the paths behind one activity row.
//
// The journal counts what a sync did; this answers which files it was. That
// question already has an exact answer — each sync is one commit in the staging
// repo — so this reads git rather than recording paths in the journal, which
// would bloat a bounded log with data git already holds losslessly.
func (a *Activity) Files(ctx context.Context, at time.Time, machine string) (EventFiles, error) {
	staging, err := config.StagingDir()
	if err != nil {
		return EventFiles{}, err
	}
	repo, err := gitrepo.Open(ctx, staging)
	if err != nil {
		return EventFiles{}, err
	}
	sha, ok, err := repo.CommitAt(ctx, at, "clauderig sync: "+machine)
	if err != nil {
		return EventFiles{}, err
	}
	if !ok {
		// Ordinary, not an error: a sync that wrote nothing never commits, and
		// the newest record is often still waiting for its own commit.
		return EventFiles{Note: "No commit for this run — it had nothing to record."}, nil
	}
	files, err := repo.CommitFiles(ctx, sha)
	if err != nil {
		return EventFiles{}, err
	}
	out := EventFiles{Commit: shortSHA(sha)}
	if len(files) > MaxEventFiles {
		out.Truncated = len(files) - MaxEventFiles
		files = files[:MaxEventFiles]
	}
	out.Files = files
	return out, nil
}

func shortSHA(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return strings.TrimSpace(s)
}
