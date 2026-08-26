package engine

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rigsmith/rigsmith/core/pathmap"
	"github.com/rigsmith/rigsmith/internal/clauderig/allowlist"
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

// effectiveRoots is the root list sync and restore actually walk: the configured
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

// LocalProfileNames lists the Desktop profiles on this machine — the ones sync
// walks as roots of their own.
//
// Best-effort: a profile store that cannot be read means this run covers the
// configured roots and nothing else, which is what clauderig did before profiles
// existed. Backing up profiles must never be the reason a sync fails.
func LocalProfileNames() []string {
	st, err := desktop.DefaultStore()
	if err != nil {
		return nil
	}
	profiles, err := st.List()
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(profiles))
	for _, p := range profiles {
		names = append(names, p.Name)
	}
	return names
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

// StagedProfileDataDir is where a staged profile's Desktop tree actually starts.
//
// A profile root is staged as desktop@<name>/ holding the profile's own
// metadata beside a data/ directory — the same shape as the live profile, whose
// Desktop tree is likewise <profile>/data. Readers that want claude-code-sessions
// must therefore descend that one level; pointing at desktop@<name> itself finds
// nothing, silently.
func StagedProfileDataDir(stagingDir, name string) string {
	return filepath.Join(stagingDir, profileRootPrefix+name, "data")
}

// desktopRel strips the wrapper a profile root adds, so the engine's rules about
// paths inside a Desktop tree ("config.json", the sidecar layout) are written
// once and hold for the machine-wide install and a profile alike.
func desktopRel(rootID, rel string) string {
	if ProfileNameOf(rootID) == "" {
		return rel
	}
	return strings.TrimPrefix(rel, "data/")
}

// desktopTreesIn lists the staged Desktop trees this run actually walked, as
// paths relative to the staging dir. A root that was skipped is left out: its
// sidecars were never offered to this run, so nothing was learned about whether
// they are orphaned.
func desktopTreesIn(rep *Report) []string {
	var trees []string
	for _, r := range rep.Roots {
		if r.Skipped || !allowlist.DesktopRoot(r.ID) {
			continue
		}
		if ProfileNameOf(r.ID) != "" {
			trees = append(trees, filepath.Join(r.ID, "data"))
			continue
		}
		trees = append(trees, r.ID)
	}
	return trees
}

// perm are the modes a restore writes with.
type perm struct {
	dir  os.FileMode
	file os.FileMode
}

var (
	// defaultPerm matches what ~/.claude and the Desktop application-support
	// tree already carry: the apps create these files themselves, and tightening
	// them on restore would diverge from what the next app write puts back.
	defaultPerm = perm{dir: 0o755, file: 0o644}
	// profilePerm matches desktop.Store, which creates profile directories 0700
	// and profile.json 0600. A restore is the one path that materialises a
	// profile without going through the store, so it has to carry the same modes
	// — otherwise a profile recreated on a fresh machine would hold its chat
	// history world-readable, which is the opposite of the invariant the store
	// exists to keep. (Unix only in effect: Go's Chmod on Windows toggles
	// read-only and nothing else, so containment there rests on the ACL
	// inherited from %USERPROFILE%.)
	profilePerm = perm{dir: 0o700, file: 0o600}
)

// permFor picks the modes a root's restored files carry.
func permFor(rootID string) perm {
	if ProfileNameOf(rootID) != "" {
		return profilePerm
	}
	return defaultPerm
}
