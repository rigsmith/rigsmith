//go:build darwin

package desktop

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// A macOS shortcut is a hand-built application bundle. A bundle is only a
// directory with an Info.plist and an executable in it, so nothing here needs a
// compiler or a signing identity: the executable is a /bin/sh script that runs
// `clauderig desktop open <profile>`.
//
// Gatekeeper does not stand in the way, and the reason is worth writing down
// because it looks like it should. Gatekeeper gates QUARANTINED bundles — ones
// carrying com.apple.quarantine, which the OS attaches to things that arrive
// from a browser, a mail client or a download. A bundle written locally by a
// process the user ran carries no such attribute and launches unsigned.
//
// The alternative — a .command file — was rejected: it opens a Terminal window
// and leaves it open, which is a strange thing to hand someone who asked for an
// icon.

const (
	appExt = ".app"
	// bundleExec is the name of the script inside the bundle. It is also what
	// Activity Monitor shows, so it says what it is rather than "launch".
	bundleExec = "clauderig-desktop-open"
	// markerFile identifies a bundle as ours. In Resources/ because it is a
	// resource; read directly rather than through Info.plist so that finding
	// our shortcuts never depends on parsing a plist.
	markerFile = "clauderig-profile"
	iconFile   = "icon.icns"
)

func destLabel(d Dest) string {
	if d == DestApps {
		return "~/Applications"
	}
	return "the Desktop"
}

func shortcutDir(d Dest) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	switch d {
	case DestDesktop:
		return filepath.Join(home, "Desktop"), nil
	case DestApps:
		return filepath.Join(home, "Applications"), nil
	}
	return "", fmt.Errorf("unknown shortcut location %q", d)
}

// installShortcutIn builds the bundle beside its final location and swaps it in.
//
// Built elsewhere first because the destination may already hold a WORKING
// shortcut: writing over it in place means a failure halfway through (a full
// disk, a missing icon source) leaves a half-built bundle where a functioning
// one used to be. The swap replaces one complete bundle with another.
func installShortcutIn(dir string, spec ShortcutSpec) (Shortcut, error) {
	spec, err := spec.normalized()
	if err != nil {
		return Shortcut{}, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Shortcut{}, err
	}
	final := filepath.Join(dir, spec.Label+appExt)

	// Lstat, not Stat: a symlink at the destination must be seen as the symlink
	// it is, not followed to whatever it points at and then deleted there.
	switch _, err := os.Lstat(final); {
	case err == nil:
		owner, ours := bundleProfile(final)
		switch {
		case !ours && !spec.Force:
			return Shortcut{}, fmt.Errorf("%w: %s", ErrShortcutExists, final)
		case ours && owner != spec.Profile && !spec.Force:
			return Shortcut{}, fmt.Errorf("%w: %s opens %q", ErrShortcutClaimed, final, owner)
		}
	case !errors.Is(err, os.ErrNotExist):
		return Shortcut{}, err
	}

	staging, err := os.MkdirTemp(dir, ".clauderig-shortcut-*")
	if err != nil {
		return Shortcut{}, err
	}
	defer func() { _ = os.RemoveAll(staging) }() // no-op once the swap succeeds
	built := filepath.Join(staging, spec.Label+appExt)
	if berr := writeBundle(built, spec); berr != nil {
		return Shortcut{}, berr
	}

	// Rename cannot replace a non-empty directory, so the old bundle moves out
	// of the way first — into the staging directory, which is deleted either
	// way. If the swap-in then fails, the old one is restored rather than lost.
	displaced := ""
	if _, serr := os.Lstat(final); serr == nil {
		displaced = filepath.Join(staging, "previous"+appExt)
		if merr := os.Rename(final, displaced); merr != nil {
			return Shortcut{}, fmt.Errorf("replace %s: %w", final, merr)
		}
	}
	if rerr := os.Rename(built, final); rerr != nil {
		if displaced != "" {
			_ = os.Rename(displaced, final)
		}
		return Shortcut{}, fmt.Errorf("install %s: %w", final, rerr)
	}
	return Shortcut{Profile: spec.Profile, Label: spec.Label, Path: final, Dest: spec.Dest}, nil
}

func writeBundle(root string, spec ShortcutSpec) error {
	contents := filepath.Join(root, "Contents")
	macOS := filepath.Join(contents, "MacOS")
	resources := filepath.Join(contents, "Resources")
	for _, d := range []string{macOS, resources} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	files := []struct {
		path string
		body string
		mode os.FileMode
	}{
		{filepath.Join(contents, "Info.plist"), infoPlist(spec), 0o644},
		// Ancient, and still what LaunchServices looks for to recognise a
		// bundle as an application before it has been registered.
		{filepath.Join(contents, "PkgInfo"), "APPL????", 0o644},
		{filepath.Join(macOS, bundleExec), launcherScript(spec), 0o755},
		{filepath.Join(resources, markerFile), shortcutMarker + "\n" + spec.Profile + "\n", 0o644},
	}
	for _, f := range files {
		if err := os.WriteFile(f.path, []byte(f.body), f.mode); err != nil {
			return err
		}
	}
	// The icon is a nicety: a bundle without one gets the generic app icon and
	// still works, so a Claude install we cannot read must not fail the write.
	if src, ok := claudeIconPath(); ok {
		icon := filepath.Join(resources, iconFile)
		if _, err := copyFile(src, icon, 0o644); err != nil {
			// A truncated .icns renders worse than none: Finder shows a broken
			// icon rather than falling back to the generic one.
			_ = os.Remove(icon)
		}
	}
	return nil
}

// infoPlist is the bundle's metadata. Written by hand rather than through a
// plist library: it is four fixed keys and two variable ones, and the only
// thing that needs care is escaping the label, which arrives from a --label
// flag and lands between XML tags.
func infoPlist(spec ShortcutSpec) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	b.WriteString(`<plist version="1.0">` + "\n<dict>\n")
	pairs := [][2]string{
		{"CFBundleName", spec.Label},
		{"CFBundleDisplayName", spec.Label},
		{"CFBundleExecutable", bundleExec},
		{"CFBundleIdentifier", bundleID(spec.Profile)},
		{"CFBundleIconFile", iconFile},
		{"CFBundlePackageType", "APPL"},
		{"CFBundleInfoDictionaryVersion", "6.0"},
		{"CFBundleShortVersionString", "1.0"},
		{"CFBundleVersion", "1"},
		// Our own key, for anyone who opens the plist wondering what this is.
		// The marker FILE remains what identification reads.
		{"ClauderigDesktopProfile", spec.Profile},
	}
	for _, kv := range pairs {
		fmt.Fprintf(&b, "\t<key>%s</key>\n\t<string>%s</string>\n", kv[0], xmlEscape(kv[1]))
	}
	// The wrapper exits the moment it has handed off to `open`, so a bouncing
	// Dock tile for it would be noise. LSUIElement keeps it out of the Dock;
	// the Claude window it starts is unaffected, and osascript alerts still
	// show, because they are a separate process.
	b.WriteString("\t<key>LSUIElement</key>\n\t<true/>\n")
	b.WriteString("</dict>\n</plist>\n")
	return b.String()
}

func xmlEscape(s string) string {
	var buf bytes.Buffer
	_ = xml.EscapeText(&buf, []byte(s))
	return buf.String()
}

// bundleID keeps every shortcut's identifier legal and DISTINCT. A bundle
// identifier takes alphanumerics, hyphens and dots only, while a profile name
// may also contain an underscore and a dot.
//
// Sanitising alone is not enough, and the reason is the whole point of the
// digest: mapping every illegal character to a hyphen is not injective, so
// `work_a`, `work.a` and `work-a` — three profiles a user may legitimately have
// — would share one identifier. LaunchServices keys an application on this
// identifier, so a click could then reach the wrong bundle. A short digest of
// the raw name restores uniqueness while leaving the readable part readable.
func bundleID(profile string) string {
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-':
			return r
		default:
			return '-'
		}
	}, profile)
	sum := sha256.Sum256([]byte(profile))
	return "dev.rigsmith.clauderig.desktop." + safe + "-" + hex.EncodeToString(sum[:4])
}

// launcherScript is the bundle's executable.
//
// The error path matters more than the happy one. A GUI launch has nowhere to
// print: a failing script would simply do nothing, and the user would be left
// clicking an icon that does not respond. So both failures — clauderig missing,
// and `desktop open` refusing (an uninstalled Claude, an ambiguous deep link, a
// deleted profile) — surface as an alert carrying the actual message.
//
// osascript is given the strings as ARGUMENTS rather than interpolated into the
// AppleScript source, because the second one is clauderig's own error text:
// arbitrary, multi-line, and full of quotes.
func launcherScript(spec ShortcutSpec) string {
	return "#!/bin/sh\n" +
		"# Claude Desktop profile launcher, written by clauderig.\n" +
		"# " + shortcutTag(spec.Profile) + "\n" +
		"# Rewrite it with: clauderig desktop shortcut " + spec.Profile + "\n" +
		"\n" +
		"CLAUDERIG=" + shQuote(spec.Exe) + "\n" +
		"PROFILE=" + shQuote(spec.Profile) + "\n" +
		"\n" +
		"alert() {\n" +
		"\t/usr/bin/osascript -e 'on run argv\n" +
		"\t\tdisplay alert (item 1 of argv) message (item 2 of argv) as critical\n" +
		"\tend run' \"$1\" \"$2\" >/dev/null 2>&1\n" +
		"}\n" +
		"\n" +
		"if [ ! -x \"$CLAUDERIG\" ]; then\n" +
		"\talert \"clauderig is not where this shortcut expects it\" \\\n" +
		"\t\t\"This shortcut runs $CLAUDERIG, which is missing. Reinstall clauderig, then run: clauderig desktop shortcut $PROFILE\"\n" +
		"\texit 1\n" +
		"fi\n" +
		"\n" +
		"if err=$(\"$CLAUDERIG\" desktop open \"$PROFILE\" 2>&1); then\n" +
		"\texit 0\n" +
		"fi\n" +
		"alert \"Could not open the $PROFILE Claude Desktop profile\" \"$err\"\n" +
		"exit 1\n"
}

// shQuote wraps a value for /bin/sh. Single quotes take everything literally;
// the only character that needs work is the single quote itself.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// claudeIconPath finds the installed app's icon to copy into the bundle.
//
// The app's own Info.plist is asked first, because that is the only answer that
// stays right when Claude renames the file (today it ships Electron's default
// name, electron.icns). The guesses below it are the fallback for a bundle whose
// plist cannot be read, and they are tried in order before the glob so a bundle
// carrying several .icns files — document icons, for one — yields the app icon
// rather than whichever sorted first.
func claudeIconPath() (string, bool) {
	bundle, ok := darwinApp{}.Installed()
	if !ok {
		return "", false
	}
	res := filepath.Join(bundle, "Contents", "Resources")
	if named, derr := exec.Command("/usr/bin/plutil", "-extract", "CFBundleIconFile", "raw",
		filepath.Join(bundle, "Contents", "Info.plist")).Output(); derr == nil {
		name := strings.TrimSpace(string(named))
		// The key may or may not carry the extension; both spellings are legal.
		if name != "" && !strings.Contains(name, "/") {
			if !strings.HasSuffix(name, ".icns") {
				name += ".icns"
			}
			if fi, serr := os.Stat(filepath.Join(res, name)); serr == nil && !fi.IsDir() {
				return filepath.Join(res, name), true
			}
		}
	}
	for _, name := range []string{"AppIcon.icns", "icon.icns", "Claude.icns", "electron.icns"} {
		p := filepath.Join(res, name)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p, true
		}
	}
	matches, err := filepath.Glob(filepath.Join(res, "*.icns"))
	if err != nil || len(matches) == 0 {
		return "", false
	}
	return matches[0], true
}

// bundleProfile reads the marker out of a bundle, reporting whether clauderig
// wrote it and for which profile.
func bundleProfile(appPath string) (string, bool) {
	raw, err := os.ReadFile(filepath.Join(appPath, "Contents", "Resources", markerFile))
	if err != nil {
		return "", false
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) < 2 || strings.TrimSpace(lines[0]) != shortcutMarker {
		return "", false
	}
	name := strings.TrimSpace(lines[1])
	// Re-validated because this is about to be compared against, and deleted
	// with, a profile name — from a file on disk that anyone may have edited.
	if ValidName(name) != nil {
		return "", false
	}
	return name, true
}

func listShortcutsIn(dir string) ([]Shortcut, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Shortcut
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, appExt) {
			continue
		}
		path := filepath.Join(dir, name)
		profile, ok := bundleProfile(path)
		if !ok {
			continue // somebody else's app, or a half-written one
		}
		out = append(out, Shortcut{
			Profile: profile,
			Label:   strings.TrimSuffix(name, appExt),
			Path:    path,
		})
	}
	return out, nil
}

// removeShortcutAt deletes a bundle. RemoveAll because a bundle is a directory,
// and the caller only ever passes paths that came back from listShortcutsIn —
// which returns nothing that does not carry our marker.
func removeShortcutAt(path string) error { return os.RemoveAll(path) }
