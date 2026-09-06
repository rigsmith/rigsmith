// Package adapter describes Claude's root and artifact policies for capture,
// restore and publication. These are internal, in-memory decisions; they do not
// change the backup format or define a vendor-neutral API yet.
package adapter

import (
	"strings"

	"github.com/rigsmith/rigsmith/core/pathmap"
	"github.com/rigsmith/rigsmith/internal/clauderig/allowlist"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
)

type RootKind uint8

const (
	CLIRoot RootKind = iota
	DesktopRoot
	DesktopProfileRoot
)

// Root keeps the configured identity/location and the allowlist used for both
// the live walk and staged reconciliation. Unknown IDs retain the CLI rules.
type Root struct {
	config.Root
	Kind      RootKind
	Allowlist allowlist.List
}

func DescribeRoot(root config.Root) Root {
	return Root{Root: root, Kind: rootKind(root.ID), Allowlist: allowlist.For(root.ID)}
}

func rootKind(id string) RootKind {
	if strings.HasPrefix(id, profileRootPrefix) {
		return DesktopProfileRoot
	}
	if id == DesktopRootID {
		return DesktopRoot
	}
	return CLIRoot
}

// Roots preserves configured order and appends profiles with Desktop's enabled
// setting. It does not discover profiles or resolve paths on the caller's behalf.
func Roots(cfg *config.Config, profiles []string) []Root {
	configured := EffectiveRoots(cfg, profiles)
	roots := make([]Root, 0, len(configured))
	for _, root := range configured {
		roots = append(roots, DescribeRoot(root))
	}
	return roots
}

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

// profileDirTemplate is a profile's directory as a portable path.
//
// Deliberately a $HOME-relative template rather than the absolute directory the
// local profile store reports: the layout is identical on every OS, so this
// resolves on the machine restoring as readily as on the machine that synced —
// which is what lets `restore` recreate a profile on a computer that has never
// seen it.
func profileDirTemplate(name string) string {
	return "$HOME/.clauderig/desktop/" + name
}

// profileRoot builds the synthetic root for one profile.
func profileRoot(name string, enabled bool) config.Root {
	return config.Root{
		ID:       ProfileRootID(name),
		Enabled:  enabled,
		Location: pathmap.Cascade{Portable: profileDirTemplate(name)},
	}
}

// ProfileDir resolves where the Desktop profile named name lives on machine m —
// the directory sync reads and restore writes. Its app data is under data/, and
// clauderig's own record of the profile sits beside that.
//
// Exported so the profile store and the sync engine can be checked against each
// other: they derive the same path independently, and a silent divergence would
// mean syncing a directory nothing writes to.
func ProfileDir(name string, m config.Machine) (string, pathmap.Status) {
	return profileRoot(name, true).ResolveOn(m)
}

// EffectiveRoots is the root list sync and restore actually walk: the configured
// roots, plus one per Desktop profile.
//
// The profile roots inherit the Desktop root's enabled flag. Turning Desktop
// sync off is a statement about Desktop's data, and profiles are more of it —
// so `clauderig config` keeps meaning what it says without growing a second
// switch nobody would think to look for.
func EffectiveRoots(cfg *config.Config, profiles []string) []config.Root {
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
