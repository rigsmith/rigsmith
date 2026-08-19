package engine

import (
	"os"
	"sort"
	"strings"

	"github.com/rigsmith/rigsmith/core/pathmap"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/desktop"
)

// This file makes sync and restore aware of Claude Desktop profiles.
//
// A profile is a directory Claude Desktop owns outright (`clauderig desktop`),
// and it has exactly the shape of the machine-wide install — same session trees,
// same config.json. So rather than teach the engine a second kind of tree, each
// profile is presented to it as its own sync root: same walk, same allowlist,
// same retention, same redaction, staged under its own id.
//
// clauderig never writes inside a profile to make this work. It reads the same
// allowlisted paths it already reads from the machine-wide install, and since
// that allowlist is include-only, a profile contributes nothing beyond what the
// unprofiled Desktop root already contributes. The login is not in that set —
// on any platform, by construction — so a profile's credentials never sync, and
// restoring one recreates its settings and history but never signs it in.

// DesktopRootID is the root covering the machine-wide Claude Desktop install.
const DesktopRootID = "desktop"

// profileRootPrefix marks the synthetic roots standing in for Desktop profiles.
// '@' cannot appear in a profile name (desktop.ValidName), so the prefix cannot
// collide with a real id, and it is a legal path segment on every platform —
// the id is also the staging directory name.
const profileRootPrefix = DesktopRootID + "@"

// ProfileRootID is the sync-root id for the Desktop profile named name.
func ProfileRootID(name string) string { return profileRootPrefix + name }

// ProfileNameOf recovers the profile name from a synthetic root id, or "" when
// id is not one.
func ProfileNameOf(id string) string {
	name, ok := strings.CutPrefix(id, profileRootPrefix)
	if !ok {
		return ""
	}
	return name
}

// isDesktopTree reports whether a root holds a Claude Desktop application-support
// tree — the machine-wide install or any profile. Everything the engine special-
// cases for Desktop (its allowlist, the config.json keep-filter, sidecar
// retention) keys off this rather than off the bare "desktop" id.
func isDesktopTree(rootID string) bool {
	return rootID == DesktopRootID || strings.HasPrefix(rootID, profileRootPrefix)
}

// profileDataTemplate is a profile's data directory as a portable path.
//
// Deliberately a $HOME-relative template rather than the absolute directory the
// local profile store reports: the layout is identical on every OS, so this
// resolves on the machine restoring as readily as on the machine that synced —
// which is what lets `restore` recreate a profile on a computer that has never
// seen it.
func profileDataTemplate(name string) string {
	return "$HOME/.clauderig/desktop/" + name + "/data"
}

// profileRoot builds the synthetic root for one profile.
func profileRoot(name string, enabled bool) config.Root {
	return config.Root{
		ID:       ProfileRootID(name),
		Enabled:  enabled,
		Location: pathmap.Cascade{Portable: profileDataTemplate(name)},
	}
}

// ProfileDataDir resolves where the Desktop profile named name keeps its data on
// machine m — the directory sync reads and restore writes.
//
// Exported so the profile store and the sync engine can be checked against each
// other: they derive the same path independently, and a silent divergence would
// mean syncing a directory nothing writes to.
func ProfileDataDir(name string, m config.Machine) (string, pathmap.Status) {
	return profileRoot(name, true).ResolveOn(m)
}

// effectiveRoots is the root list sync and restore actually walk: the configured
// roots, plus one per Desktop profile.
//
// The profile roots inherit the Desktop root's enabled flag. Turning Desktop
// sync off is a statement about Desktop's data, and profiles are more of it —
// so `clauderig config` keeps meaning what it says without growing a second
// switch nobody would think to look for.
func effectiveRoots(cfg *config.Config, profiles []string) []config.Root {
	roots := cfg.Roots
	if len(profiles) == 0 {
		return roots
	}
	enabled := false
	for _, r := range cfg.Roots {
		if r.ID == DesktopRootID {
			enabled = r.Enabled
			break
		}
	}
	out := make([]config.Root, 0, len(roots)+len(profiles))
	out = append(out, roots...)
	for _, name := range profiles {
		out = append(out, profileRoot(name, enabled))
	}
	return out
}

// StagedProfileNames lists the Desktop profiles present in a staging tree.
//
// This is how `restore` learns which profiles to write: the repo is the record,
// not the local machine, so a computer that has never run `clauderig desktop`
// still gets every profile back.
func StagedProfileNames(stagingDir string) []string {
	entries, err := os.ReadDir(stagingDir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		// Validated, not merely parsed: the name is concatenated into the
		// restore target path, and the staging tree is a git checkout — so a
		// directory named `desktop@..` must not be able to steer a write out of
		// the profile store.
		if name := ProfileNameOf(e.Name()); name != "" && desktop.ValidName(name) == nil {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// desktopTreesIn lists the Desktop roots this run actually walked — the ids
// whose staged trees the sidecar pass may judge. A root that was skipped is left
// out: its sidecars were never offered to this run, so nothing was learned about
// whether they are orphaned.
func desktopTreesIn(rep *Report) []string {
	var ids []string
	for _, r := range rep.Roots {
		if !r.Skipped && isDesktopTree(r.ID) {
			ids = append(ids, r.ID)
		}
	}
	return ids
}
