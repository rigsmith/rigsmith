package engine

import (
	"os"
	"path/filepath"
	"sort"

	"github.com/rigsmith/rigsmith/core/pathmap"
	"github.com/rigsmith/rigsmith/internal/agentrig/files"
	"github.com/rigsmith/rigsmith/internal/clauderig/adapter"
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

// These entry points remain available to existing Claude commands and callers;
// root policy itself lives in the adapter.
const DesktopRootID = adapter.DesktopRootID
const profileRootPrefix = DesktopRootID + "@"

func ProfileRootID(name string) string { return adapter.ProfileRootID(name) }
func ProfileNameOf(id string) string   { return adapter.ProfileNameOf(id) }
func ProfileDir(name string, m config.Machine) (string, pathmap.Status) {
	return adapter.ProfileDir(name, m)
}
func EffectiveRoots(cfg *config.Config, profiles []string) []config.Root {
	return adapter.EffectiveRoots(cfg, profiles)
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

// StagedProfileDataDir is where a staged profile's Desktop tree starts: the
// profile's own metadata sits beside a data/ directory, mirroring the live
// layout. Readers must descend that level — pointing at desktop@<name> itself
// finds nothing, silently.
func StagedProfileDataDir(stagingDir, name string) string {
	return filepath.Join(stagingDir, profileRootPrefix+name, "data")
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
type perm = files.Permissions

var (
	// defaultPerm matches what ~/.claude and the Desktop application-support
	// tree already carry: the apps create these files themselves, and tightening
	// them on restore would diverge from what the next app write puts back.
	defaultPerm = perm{Dir: 0o755, File: 0o644}
	// profilePerm matches desktop.Store, which creates profile directories 0700
	// and profile.json 0600. A restore is the one path that materialises a
	// profile without going through the store, so it has to carry the same modes
	// — otherwise a profile recreated on a fresh machine would hold its chat
	// history world-readable, which is the opposite of the invariant the store
	// exists to keep. (Unix only in effect: Go's Chmod on Windows toggles
	// read-only and nothing else, so containment there rests on the ACL
	// inherited from %USERPROFILE%.)
	profilePerm = perm{Dir: 0o700, File: 0o600}
)

// permFor picks the modes a root's restored files carry.
func permFor(rootID string) perm {
	if ProfileNameOf(rootID) != "" {
		return profilePerm
	}
	return defaultPerm
}
