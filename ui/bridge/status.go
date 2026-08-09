// Package bridge is the read half of the engine seam: it calls
// internal/clauderig/... in-process and flattens the result into structs the
// frontend can hold. No sync logic lives here — anything with a side effect
// shells out to the clauderig binary instead, so the CLI stays the single
// implementation of everything that can lose data.
package bridge

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/health"
	"github.com/rigsmith/rigsmith/internal/clauderig/journal"
	"github.com/rigsmith/rigsmith/internal/clauderig/status"
)

// settingsPath locates ~/.claude/settings.json, which status.Gather reads for
// hook state. It prefers the detected machine's home so it agrees with the rest
// of clauderig's path resolution.
func settingsPath(me config.Machine) (string, error) {
	home := me.Home
	if home == "" {
		var err error
		if home, err = os.UserHomeDir(); err != nil {
			return "", err
		}
	}
	if home == "" {
		return "", errors.New("bridge: cannot locate home directory")
	}
	return filepath.Join(home, ".claude", "settings.json"), nil
}

// Device is one machine on the board.
type Device struct {
	Name          string    `json:"name"`
	OS            string    `json:"os"`
	LastSync      time.Time `json:"lastSync"`
	ClaudeVersion string    `json:"claudeVersion,omitempty"`
	This          bool      `json:"this"`
}

// Root is one synced tree's local state.
type Root struct {
	ID      string `json:"id"`
	Files   int    `json:"files"`
	Present bool   `json:"present"`
}

// Snapshot is everything the tray and the status window render.
type Snapshot struct {
	Machine  string    `json:"machine"`
	OS       string    `json:"os"`
	Remote   string    `json:"remote"`
	LastSync string    `json:"lastSync"`
	Level    string    `json:"level"`  // "green" | "amber" | "red"
	Reason   string    `json:"reason"` // stable token the frontend branches on
	Summary  string    `json:"summary"`
	Action   string    `json:"action,omitempty"`
	Ahead    int       `json:"ahead"`
	Behind   int       `json:"behind"`
	Dirty    bool      `json:"dirty"`
	Roots    []Root    `json:"roots"`
	Hooks    []string  `json:"hooks"`
	Devices  []Device  `json:"devices"`
	TakenAt  time.Time `json:"takenAt"`
}

// Status is the service bound to the frontend.
type Status struct {
	// now is injected so tests can pin TakenAt.
	now func() time.Time
}

// NewStatus builds the status service.
func NewStatus() *Status { return &Status{now: time.Now} }

// Get gathers a fresh snapshot. status.Gather does local work only — no network
// — so this is safe to poll on the tray's 30–60s tick without blocking on an
// unreachable remote.
func (s *Status) Get(ctx context.Context) (Snapshot, error) {
	cfg, err := config.LoadOrDefault()
	if err != nil {
		return Snapshot{}, err
	}
	me := config.DetectFor(cfg)
	staging, _ := config.StagingDir()
	settings, _ := settingsPath(me)

	return s.snapshot(status.Gather(ctx, cfg, me, staging, settings), lastRun(staging)), nil
}

// Health is the tray's hot path: the level only, without marshalling the board.
func (s *Status) Health(ctx context.Context) (health.Report, error) {
	cfg, err := config.LoadOrDefault()
	if err != nil {
		return health.Report{}, err
	}
	me := config.DetectFor(cfg)
	staging, _ := config.StagingDir()
	settings, _ := settingsPath(me)

	return health.Of(status.Gather(ctx, cfg, me, staging, settings), lastRun(staging)), nil
}

// lastRun returns the most recent journalled operation, or the zero Record when
// nothing has been recorded. Only the newest matters: a failure three days ago
// that has since been followed by a clean sync is history, not a current state.
func lastRun(staging string) journal.Record {
	recs, err := journal.Read(staging, 1)
	if err != nil || len(recs) == 0 {
		return journal.Record{}
	}
	return recs[0]
}

// snapshot flattens an Info into the frontend's shape. Split out from Get so
// the mapping is testable without a real ~/.claude on disk.
func (s *Status) snapshot(info status.Info, last journal.Record) Snapshot {
	rep := health.Of(info, last)

	snap := Snapshot{
		Machine:  info.Machine.Name,
		OS:       info.Machine.OS,
		Remote:   info.Remote,
		LastSync: info.LastSync,
		Level:    rep.Level.String(),
		Reason:   rep.Reason.String(),
		Summary:  rep.Summary,
		Action:   rep.Action,
		Ahead:    rep.Ahead,
		Behind:   rep.Behind,
		Dirty:    info.Dirty,
		Hooks:    info.Hooks,
		TakenAt:  s.now(),
	}
	for _, r := range info.Roots {
		snap.Roots = append(snap.Roots, Root{ID: r.ID, Files: r.Files, Present: r.Present})
	}
	for _, d := range info.Devices {
		snap.Devices = append(snap.Devices, Device{
			Name:          d.Name,
			OS:            d.OS,
			LastSync:      d.LastSync,
			ClaudeVersion: d.ClaudeVersion,
			This:          d.Name == info.Machine.Name,
		})
	}
	return snap
}
