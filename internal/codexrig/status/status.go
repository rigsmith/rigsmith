// Package status gathers one local snapshot of how the sync is doing, and
// judges it. Everything here is local — no network — so a dashboard can poll it
// without a hung remote hanging the screen.
//
// The judgement is separate from the gathering, and the reason is a real
// incident in the sibling tool: every surface keyed off the last PUSH, so a
// machine sixty-five commits behind reported "synced five minutes ago" for a
// day. Being up to date is about the remote's work reaching here as much as
// about this machine's work reaching the remote, and Level says so.
package status

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/core/pathmap"
	"github.com/rigsmith/rigsmith/internal/agentrig/allowlist"
	cxallow "github.com/rigsmith/rigsmith/internal/codexrig/allowlist"
	"github.com/rigsmith/rigsmith/internal/codexrig/config"
	"github.com/rigsmith/rigsmith/internal/codexrig/devices"
	"github.com/rigsmith/rigsmith/internal/codexrig/journal"
)

// RootInfo is one sync root as it stands on this machine.
type RootInfo struct {
	ID      string `json:"id"`
	Files   int    `json:"files"`
	Present bool   `json:"present"`
}

// AccountInfo is the login this machine is syncing as.
type AccountInfo struct {
	Email     string `json:"email,omitempty"`
	Plan      string `json:"plan,omitempty"`
	Alias     string `json:"alias,omitempty"`
	LoggedOut bool   `json:"loggedOut,omitempty"`
	Problem   string `json:"problem,omitempty"`
}

// Info is the snapshot.
type Info struct {
	Machine       config.Machine     `json:"machine"`
	Remote        string             `json:"remote"`
	HasStaging    bool               `json:"hasStaging"`
	LastSync      string             `json:"lastSync"`
	Dirty         bool               `json:"dirty"`
	SyncEvery     time.Duration      `json:"syncEvery"`
	Divergence    gitrepo.Divergence `json:"divergence"`
	Unpushed      int                `json:"unpushed"`
	Unmerged      int                `json:"unmerged"`
	TrackingKnown bool               `json:"trackingKnown"`
	Roots         []RootInfo         `json:"roots"`
	Devices       []devices.Device   `json:"devices"`
	Account       AccountInfo        `json:"account"`
	Sessions      bool               `json:"sessions"`
}

// TrackingRef is the ref divergence is measured against.
const TrackingRef = "origin/main"

// Gather reads everything locally available. It never touches the network.
func Gather(ctx context.Context, cfg *config.Config, me config.Machine, staging string, acct AccountInfo) Info {
	info := Info{
		Machine: me, Remote: cfg.Remote, SyncEvery: cfg.HookInterval(),
		Account: acct, Sessions: cfg.SyncSessions,
	}
	if _, err := os.Stat(filepath.Join(staging, ".git")); err == nil {
		info.HasStaging = true
		if repo, err := gitrepo.Open(ctx, staging); err == nil {
			if h, subject, when, err := repo.LastCommit(ctx); err == nil {
				info.LastSync = fmt.Sprintf("%s %s — %s", h, when, subject)
			}
			// The journal is excluded: a pending journal line is append-only
			// bookkeeping the next sync sweeps up, and counting it leaves every
			// surface amber after a perfectly good restore.
			info.Dirty, _ = repo.DirtyExcluding(ctx, journal.DirName)
			if d, err := repo.DivergenceFrom(ctx, TrackingRef); err == nil {
				info.Divergence = d
				info.Unpushed, info.Unmerged, info.TrackingKnown = d.Ahead, d.Behind, d.Tracked
			}
		}
	}
	lists := cxallow.Options{Sessions: cfg.SyncSessions}
	for _, r := range cfg.Roots {
		ri := RootInfo{ID: r.ID}
		if loc, st := r.ResolveOn(me); st == pathmap.StatusResolved {
			if fi, err := os.Stat(loc); err == nil && fi.IsDir() {
				ri.Present = true
				if files, _, err := allowlist.Walk(loc, cxallow.For(r.ID, lists)); err == nil {
					ri.Files = len(files)
				}
			}
		}
		info.Roots = append(info.Roots, ri)
	}
	if reg, err := devices.Load(staging); err == nil {
		info.Devices = reg.List()
	}
	return info
}

// Level is how worried to be.
type Level int

const (
	Green Level = iota
	Amber
	Red
)

func (l Level) String() string {
	switch l {
	case Amber:
		return "amber"
	case Red:
		return "red"
	default:
		return "green"
	}
}

// Report is the judgement: one level, one stable reason token, one sentence, and
// the command that would fix it.
type Report struct {
	Level   Level  `json:"level"`
	Reason  string `json:"reason"`
	Summary string `json:"summary"`
	Action  string `json:"action,omitempty"`
}

// Judge turns a snapshot and the last run into a verdict.
//
// The branch order IS the priority order: the worst true thing wins, because
// there is only one colour to spend.
func Judge(info Info, last journal.Record, haveLast bool) Report {
	switch {
	case !info.HasStaging:
		return Report{Amber, "unconfigured", "No sync repo yet", "codexrig init"}
	case info.Divergence.Merging:
		return Report{Red, "merging", "A merge is in progress", "codexrig sync"}
	case info.Divergence.Diverged():
		return Report{Red, "diverged", "This machine and the remote have both moved on", "codexrig sync"}
	case haveLast && last.Outcome == journal.OutcomeRefused:
		return Report{Red, "last-run-refused", last.Summary(), "codexrig doctor"}
	case haveLast && last.Outcome == journal.OutcomeFailed:
		return Report{Red, "last-run-failed", last.Summary(), "codexrig sync"}
	case info.Remote != "" && !info.TrackingKnown:
		return Report{Amber, "never-fetched", "Never fetched " + TrackingRef, "codexrig pull"}
	case info.Unmerged > 0:
		return Report{Amber, "behind", fmt.Sprintf("%d commit(s) on the remote are not here yet", info.Unmerged), "codexrig pull"}
	case info.Unpushed > 0:
		return Report{Amber, "ahead", fmt.Sprintf("%d commit(s) have not reached the remote", info.Unpushed), "codexrig sync"}
	case info.Dirty && !overdue(info, last, haveLast):
		// Staged-but-uncommitted between two hook runs is the normal state, not
		// a fault. It only becomes one once a sync should have happened and did
		// not.
		return Report{Green, "synced", "Up to date", ""}
	case info.Dirty:
		return Report{Amber, "uncommitted", "Captured work has not been committed", "codexrig sync"}
	default:
		return Report{Green, "synced", "Up to date", ""}
	}
}

// overdue reports whether a sync should have happened by now. Generous by a
// whole interval: the hook fires on session events, not on a timer, so a quiet
// afternoon is not a fault.
func overdue(info Info, last journal.Record, haveLast bool) bool {
	if !haveLast || info.SyncEvery <= 0 {
		return false
	}
	return time.Since(last.At) > 2*info.SyncEvery
}
