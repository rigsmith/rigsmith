package desktop

// Clickable launchers for a profile: a `.app` bundle on macOS, a `.lnk` on
// Windows, sitting on the Desktop or in ~/Applications / the Start Menu.
//
// Every shortcut runs `clauderig desktop open <profile>` rather than launching
// Claude Desktop with the profile flag directly. That costs an extra process on
// each click and buys the three things the flag alone cannot do: a second click
// FOCUSES the open window instead of starting a second instance on the same
// profile (which is the hazard the whole package is built around), `lastOpened`
// stays true, and a profile that has never been launched still gets its seeding
// and its store bookkeeping. The shortcut is a click-sized `desktop open`, not a
// parallel launch path that has to be kept in step with one.
//
// clauderig is named by ABSOLUTE PATH inside the shortcut, because a GUI launch
// inherits none of the shell's PATH. That is also the one thing that can rot:
// move or uninstall the binary and the shortcut points at nothing. Both
// platforms therefore fail loudly rather than silently — macOS shows an alert
// naming the missing path, Windows shows the shell's own "target unavailable"
// dialog — and `clauderig desktop shortcut <name>` rewrites them.
//
// Finding a shortcut again (to remove it with the profile, or to replace it) is
// done by reading a MARKER out of the artifact itself, never from a manifest
// beside it. A manifest goes stale the moment someone renames or moves an icon,
// and a stale manifest is what turns "remove this profile's shortcuts" into
// "delete a file that is no longer ours".

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unicode"
)

// Dest is where a shortcut is written.
type Dest string

const (
	// DestDesktop is the user's desktop, on both platforms.
	DestDesktop Dest = "desktop"
	// DestApps is the platform's app list: ~/Applications on macOS (so the
	// shortcut is in Spotlight and Launchpad), the Start Menu on Windows.
	DestApps Dest = "apps"
)

// AllDests lists every destination, in the order a listing shows them.
func AllDests() []Dest { return []Dest{DestDesktop, DestApps} }

// ParseDest reads a --to value.
func ParseDest(s string) (Dest, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case string(DestDesktop):
		return DestDesktop, nil
	case string(DestApps), "applications", "start-menu", "startmenu", "menu":
		return DestApps, nil
	}
	return "", fmt.Errorf("unknown shortcut location %q — use `desktop` or `apps`", s)
}

// Where names this destination the way the current platform's user would.
func (d Dest) Where() string { return destLabel(d) }

// Shortcut is one installed launcher.
type Shortcut struct {
	Profile string // the profile it opens
	Label   string // its display name (the file name, without the extension)
	Path    string // the .app bundle or .lnk on disk
	Dest    Dest
}

// ShortcutSpec is a request to create one.
type ShortcutSpec struct {
	// Profile is the profile name, as `clauderig desktop open` takes it.
	Profile string
	// Label is the display name. Empty means DefaultShortcutLabel.
	Label string
	Dest  Dest
	// Exe is the absolute path to the clauderig binary the shortcut runs.
	Exe string
	// Force replaces a file at the same path that clauderig did not write.
	Force bool
}

// ErrShortcutExists means something already occupies the shortcut's path and it
// is not one of ours — so replacing it would destroy someone else's file.
var ErrShortcutExists = errors.New("a file that clauderig did not create is already there")

// ShortcutsSupported reports whether this platform has shortcuts to create. It
// tracks Supported() exactly: there is no Claude Desktop to point at otherwise.
func ShortcutsSupported() bool { return Supported() }

// DefaultShortcutLabel is what a shortcut is called when the user does not say.
// Deliberately plain ASCII with a spaced hyphen: it becomes a file name on two
// filesystems and a Spotlight/Start Menu search term, and "Claude work" should
// find it.
func DefaultShortcutLabel(profile string) string { return "Claude - " + profile }

// shortcutMarker stamps an artifact as clauderig's. It appears in the macOS
// bundle's marker file and in the Windows shortcut's description.
const shortcutMarker = "clauderig-desktop-profile"

// shortcutTag is the machine-readable form embedded in a one-line description.
func shortcutTag(profile string) string { return "[" + shortcutMarker + ":" + profile + "]" }

// shortcutDescription is the tooltip a Windows shortcut carries: a sentence for
// the person hovering over it, with the tag that identifies it appended.
func shortcutDescription(profile string) string {
	return fmt.Sprintf("Opens the %q Claude Desktop profile through clauderig %s",
		profile, shortcutTag(profile))
}

// profileFromTag pulls the profile name back out of a description, and reports
// whether the text carried one of our tags at all.
//
// The name is re-validated on the way out. This text comes off disk, from a
// file anyone may have edited, and it is about to be handed to Remove — which
// concatenates it into a path.
func profileFromTag(s string) (string, bool) {
	open := strings.Index(s, "["+shortcutMarker+":")
	if open < 0 {
		return "", false
	}
	rest := s[open+len(shortcutMarker)+2:]
	end := strings.IndexByte(rest, ']')
	if end < 0 {
		return "", false
	}
	name := rest[:end]
	if ValidName(name) != nil {
		return "", false
	}
	return name, true
}

// reservedNames are the DOS device names Windows still refuses as file names,
// with or without an extension. A label that hits one would fail at Save time
// with an opaque COM error, so it is rejected here with a readable one.
var reservedNames = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true,
	"COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true,
	"LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

// ValidLabel reports whether a label is safe to use as a file name on BOTH
// platforms — not just this one. A shortcut label is stored nowhere but the file
// name, and the profiles it names are synced between a Mac and a Windows box, so
// a label accepted on one that cannot exist on the other is a trap laid for the
// other machine.
func ValidLabel(label string) error {
	if strings.TrimSpace(label) == "" {
		return errors.New("shortcut label is empty")
	}
	if len([]rune(label)) > 64 {
		return fmt.Errorf("shortcut label %q is too long (max 64 characters)", label)
	}
	if label != strings.TrimSpace(label) {
		return fmt.Errorf("shortcut label %q has leading or trailing spaces", label)
	}
	// Windows silently strips a trailing dot, which would rename the file out
	// from under the marker lookup.
	if strings.HasSuffix(label, ".") {
		return fmt.Errorf("shortcut label %q ends in a dot", label)
	}
	if strings.HasPrefix(label, ".") {
		return fmt.Errorf("shortcut label %q starts with a dot — it would be hidden", label)
	}
	for _, r := range label {
		if unicode.IsControl(r) {
			return fmt.Errorf("shortcut label %q contains a control character", label)
		}
		if strings.ContainsRune(`<>:"/\|?*`, r) {
			return fmt.Errorf(`shortcut label %q contains %q — not allowed in a file name (<>:"/\|?* are all out)`, label, r)
		}
	}
	base, _, _ := strings.Cut(label, ".")
	if reservedNames[strings.ToUpper(base)] {
		return fmt.Errorf("shortcut label %q is a reserved Windows device name", label)
	}
	return nil
}

// normalized fills in the defaults and refuses a spec that cannot safely be
// written. Idempotent, and applied by BOTH the exported entry point and each
// platform's writer — the writer would otherwise happily produce a bundle
// called ".app" for a spec that reached it without a label.
func (s ShortcutSpec) normalized() (ShortcutSpec, error) {
	if err := ValidName(s.Profile); err != nil {
		return s, err
	}
	if s.Label == "" {
		s.Label = DefaultShortcutLabel(s.Profile)
	}
	if err := ValidLabel(s.Label); err != nil {
		return s, err
	}
	// A GUI launch inherits no PATH and no working directory of ours, so a
	// relative or bare command would resolve to nothing — at click time, in
	// front of the user, rather than here.
	if s.Exe == "" || !filepath.IsAbs(s.Exe) {
		return s, fmt.Errorf("shortcut needs the absolute path to clauderig, got %q", s.Exe)
	}
	return s, nil
}

// InstallShortcut writes one shortcut, replacing a previous clauderig one at the
// same path.
func InstallShortcut(spec ShortcutSpec) (Shortcut, error) {
	spec, err := spec.normalized()
	if err != nil {
		return Shortcut{}, err
	}
	dir, derr := shortcutDir(spec.Dest)
	if derr != nil {
		return Shortcut{}, derr
	}
	return installShortcutIn(dir, spec)
}

// Shortcuts returns every clauderig-written shortcut on this machine.
//
// A destination that does not exist yet is not an error — nobody has put
// anything there. A destination that exists and cannot be read IS one, and is
// reported rather than folded into an empty result: the callers use this to
// decide what to delete and what to tell the user is installed, and "none
// found" and "could not look" are different answers.
func Shortcuts() ([]Shortcut, error) {
	if !ShortcutsSupported() {
		return nil, nil
	}
	var out []Shortcut
	var errs []error
	for _, d := range AllDests() {
		dir, err := shortcutDir(d)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		found, lerr := listShortcutsIn(dir)
		if lerr != nil {
			errs = append(errs, lerr)
			continue
		}
		for _, s := range found {
			s.Dest = d
			out = append(out, s)
		}
	}
	return out, errors.Join(errs...)
}

// ShortcutsFor returns the shortcuts that open one profile.
func ShortcutsFor(profile string) ([]Shortcut, error) {
	all, err := Shortcuts()
	var mine []Shortcut
	for _, s := range all {
		if s.Profile == profile {
			mine = append(mine, s)
		}
	}
	return mine, err
}

// RemoveShortcutsFor deletes every shortcut that opens one profile, and returns
// the ones it deleted.
//
// Partial success is reported as such — the deleted list plus the error —
// because this runs as part of `desktop rm`, where the profile itself is
// already gone and the user needs to know which icons are still on their
// desktop about to launch nothing.
func RemoveShortcutsFor(profile string) ([]Shortcut, error) {
	mine, err := ShortcutsFor(profile)
	var removed []Shortcut
	errs := []error{err}
	for _, s := range mine {
		if rerr := removeShortcutAt(s.Path); rerr != nil {
			errs = append(errs, fmt.Errorf("remove %s: %w", s.Path, rerr))
			continue
		}
		removed = append(removed, s)
	}
	return removed, errors.Join(errs...)
}
