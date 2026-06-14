// Package status gathers a read-only snapshot of clauderig's sync state, shared
// by the `status` command and the `ui` dashboard. It does only local work (no
// network); remote reachability is left to the caller so the TUI never blocks.
package status

import (
	"context"
	"os"
	"path/filepath"

	"github.com/rigsmith/clauderig/internal/allowlist"
	"github.com/rigsmith/clauderig/internal/config"
	"github.com/rigsmith/clauderig/internal/devices"
	"github.com/rigsmith/clauderig/internal/hooks"
	"github.com/rigsmith/core/gitrepo"
	"github.com/rigsmith/core/pathmap"
)

// RootInfo is one root's local state.
type RootInfo struct {
	ID      string
	Files   int
	Present bool
}

// Info is the gathered snapshot.
type Info struct {
	Machine    config.Machine
	Remote     string
	HasStaging bool
	LastSync   string // "hash when — subject", or "" when never
	Dirty      bool
	Roots      []RootInfo
	Hooks      []string
	Devices    []devices.Device
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
			info.Dirty, _ = repo.Dirty(ctx)
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
