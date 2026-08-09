package bridge

import (
	"context"
	"time"

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
	This  bool     `json:"this"`
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
		At:      r.At,
		Machine: r.Machine,
		Op:      string(r.Op),
		Outcome: string(r.Outcome),
		Summary: r.Summary(),
		Error:   r.Error,
		This:    r.Machine == thisMachine,
	}
	for _, l := range r.Leaks {
		e.Leaks = append(e.Leaks, l.Path+" ("+l.Kind+")")
	}
	return e
}
