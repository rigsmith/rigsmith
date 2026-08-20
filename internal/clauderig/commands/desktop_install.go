package commands

import (
	"os"
	"path/filepath"

	"github.com/rigsmith/rigsmith/core/pathmap"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/engine"
)

// desktopRootID is the sync root covering Desktop's application-support tree.
const desktopRootID = "desktop"

// desktopInstallDir resolves the machine-wide Claude Desktop application-support
// directory — the install a new profile seeds its preferences from.
//
// Read from the SAVED configuration first, since `clauderig init` may have
// pinned a non-default location, and falls back to the compiled-in platform
// default: the directory exists whether or not clauderig has been told to sync
// it. Returns "" when it cannot be resolved, because seeding is a convenience
// and must never be the reason `desktop add` fails.
func desktopInstallDir() string {
	if cfg, err := config.LoadOrDefault(); err == nil {
		me := config.Detect(machineName(cfg))
		if loc, st := cfg.RootLocation(desktopRootID, me); st == pathmap.StatusResolved && loc != "" {
			return loc
		}
	}
	m := config.Detect("")
	for _, r := range config.DefaultRoots() {
		if r.ID == desktopRootID {
			return m.Resolver().Resolve(r.Location.RawFor(m.OS)).Path
		}
	}
	return ""
}

// stagedProfilePath is the profile's backup in the staging repo, or "" when it
// has none. Used to tell the truth about what `rm` did and did not delete.
func stagedProfilePath(name string) string {
	staging, err := config.StagingDir()
	if err != nil {
		return ""
	}
	p := filepath.Join(staging, engine.ProfileRootID(name))
	if info, serr := os.Stat(p); serr != nil || !info.IsDir() {
		return ""
	}
	return p
}
