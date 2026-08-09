// Package status gathers a read-only snapshot of clauderig's sync state, shared
// by the `status` command and the `ui` dashboard. It does only local work (no
// network); remote reachability is left to the caller so the TUI never blocks.
package status

import (
	"context"
	"os"
	"path/filepath"

	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/core/pathmap"
	"github.com/rigsmith/rigsmith/internal/clauderig/allowlist"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/devices"
	"github.com/rigsmith/rigsmith/internal/clauderig/hooks"
	"github.com/rigsmith/rigsmith/internal/clauderig/journal"
)

// RootInfo is one root's local state.
type RootInfo struct {
	ID      string `json:"id"`
	Files   int    `json:"files"`
	Present bool   `json:"present"`
}

// TrackingRef is the ref the staging repo syncs against. sync and pull both
// hardcode origin/main; divergence is measured against the same pair.
const TrackingRef = "origin/main"

// Info is the gathered snapshot.
type Info struct {
	Machine    config.Machine     `json:"machine"`
	Remote     string             `json:"remote"`
	HasStaging bool               `json:"hasStaging"`
	LastSync   string             `json:"lastSync"` // "hash when — subject", or "" when never
	Dirty      bool               `json:"dirty"`
	Divergence gitrepo.Divergence `json:"divergence"` // position vs TrackingRef as of the last fetch
	Roots      []RootInfo         `json:"roots"`
	Hooks      []string           `json:"hooks"`
	Devices    []devices.Device   `json:"devices"`
}

// Gather collects the local snapshot. settingsPath points at ~/.claude/settings.json.
func Gather(ctx context.Context, cfg *config.Config, me config.Machine, staging, settingsPath string) Info {
	info := Info{Machine: me, Remote: cfg.Remote}

	if _, err := os.Stat(filepath.Join(staging, ".git")); err == nil {
		info.HasStaging = true
		if repo, err := gitrepo.Open(ctx, staging); err == nil {
			if h, subj, when, err := repo.LastCommit(ctx); err == nil {
				info.LastSync = h + " " + when + " — " + subj
			}
			// The journal is excluded on purpose. Dirty means "a sync started
			// and didn't finish" — loose synced content. A pending journal line
			// isn't that: it's append-only bookkeeping the next sync sweeps up
			// on its own. Counting it would leave the tray amber after every
			// restore, which is the same species of misleading indicator this
			// work set out to remove.
			info.Dirty, _ = repo.DirtyExcluding(ctx, journal.DirName)
			// Reads the object store only — no fetch — so this stays local and
			// safe to poll. It therefore reports divergence as of the last fetch,
			// which is exactly what a stale registry could not express.
			info.Divergence, _ = repo.DivergenceFrom(ctx, TrackingRef)
		}
	}

	for _, r := range cfg.Roots {
		if !r.Enabled {
			continue
		}
		ri := RootInfo{ID: r.ID}
		loc, st := cfg.RootLocation(r.ID, me)
		if st == pathmap.StatusResolved && dirExists(loc) {
			ri.Present = true
			files, _ := allowlist.Walk(loc, allowlist.For(r.ID))
			ri.Files = len(files)
		}
		info.Roots = append(info.Roots, ri)
	}

	info.Hooks, _ = hooks.Status(settingsPath)

	if reg, err := devices.Load(staging); err == nil {
		info.Devices = reg.List()
	}
	return info
}

func dirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}
