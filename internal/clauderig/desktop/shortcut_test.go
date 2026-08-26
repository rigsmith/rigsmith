package desktop

import (
	"errors"
	"runtime"
	"strings"
	"testing"
)

// absExe is an absolute path this platform recognises as one: filepath.IsAbs on
// Windows wants a drive letter, so a Unix path would fail the check for the
// wrong reason.
var absExe = func() string {
	if runtime.GOOS == "windows" {
		return `C:\tools\clauderig.exe`
	}
	return "/usr/local/bin/clauderig"
}()

func TestParseDest(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want Dest
	}{
		{"desktop", DestDesktop},
		{" Desktop ", DestDesktop},
		{"apps", DestApps},
		{"applications", DestApps},
		{"start-menu", DestApps},
	} {
		got, err := ParseDest(tc.in)
		if err != nil || got != tc.want {
			t.Fatalf("ParseDest(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
		}
	}
	if _, err := ParseDest("dock"); err == nil {
		t.Fatal("ParseDest(dock) should have failed")
	}
}

// The label is the only place a shortcut's name is stored, and profiles are
// synced between machines — so a label has to be legal on BOTH platforms, not
// just the one it was typed on.
func TestValidLabelRejectsWhatWindowsCannotStore(t *testing.T) {
	bad := map[string]string{
		"empty":          "",
		"blank":          "   ",
		"slash":          "Claude/work",
		"backslash":      `Claude\work`,
		"colon":          "Claude: work",
		"pipe":           "Claude|work",
		"question":       "Claude?",
		"star":           "Claude*",
		"quote":          `Claude "work"`,
		"angle":          "Claude <work>",
		"control":        "Claude\nwork",
		"trailing dot":   "Claude.",
		"leading dot":    ".Claude",
		"trailing space": "Claude ",
		"reserved":       "NUL",
		"reserved ext":   "com1.thing",
		"too long":       strings.Repeat("x", 65),
	}
	for name, label := range bad {
		if err := ValidLabel(label); err == nil {
			t.Errorf("%s: ValidLabel(%q) accepted it", name, label)
		}
	}
	for _, ok := range []string{"Claude - work", "Claude (work)", "Claude & Co", "клод"} {
		if err := ValidLabel(ok); err != nil {
			t.Errorf("ValidLabel(%q) = %v, want ok", ok, err)
		}
	}
}

func TestDefaultShortcutLabel(t *testing.T) {
	if got := DefaultShortcutLabel("work"); got != "Claude - work" {
		t.Fatalf("DefaultShortcutLabel = %q", got)
	}
}

// The tag is how a shortcut is recognised as ours after the fact. It has to
// survive a round trip through a human-readable sentence, and it must not hand
// back a name that could escape the store when Remove concatenates it.
func TestShortcutTagRoundTrip(t *testing.T) {
	got, ok := profileFromTag(shortcutDescription("work_2"))
	if !ok || got != "work_2" {
		t.Fatalf("profileFromTag = %q, %v; want work_2", got, ok)
	}
	for _, s := range []string{
		"an ordinary shortcut",
		"[clauderig-desktop-profile:",
		"[clauderig-desktop-profile:../../etc]",
		"[clauderig-desktop-profile:..]",
		"[clauderig-desktop-profile:has/slash]",
		"[clauderig-desktop-profile:]",
	} {
		if name, ok := profileFromTag(s); ok {
			t.Errorf("profileFromTag(%q) accepted %q", s, name)
		}
	}
}

// A shortcut runs from a GUI, which inherits none of the shell's PATH — so a
// non-absolute binary is refused here rather than at click time.
func TestInstallShortcutNeedsAnAbsoluteBinary(t *testing.T) {
	for _, exe := range []string{"", "clauderig", "./clauderig"} {
		_, err := InstallShortcut(ShortcutSpec{Profile: "work", Dest: DestDesktop, Exe: exe})
		if err == nil {
			t.Fatalf("InstallShortcut with Exe=%q should have failed", exe)
		}
		if !strings.Contains(err.Error(), "absolute") {
			t.Fatalf("InstallShortcut with Exe=%q: %v; want an absolute-path complaint", exe, err)
		}
	}
}

// The profile name reaches a filesystem path and a command line, so it is
// validated before anything is written — on every platform, including the ones
// with no shortcuts at all.
func TestInstallShortcutValidatesTheProfileName(t *testing.T) {
	_, err := InstallShortcut(ShortcutSpec{Profile: "../escape", Dest: DestDesktop, Exe: absExe})
	if err == nil || !strings.Contains(err.Error(), "invalid profile name") {
		t.Fatalf("InstallShortcut(../escape) = %v; want an invalid-name error", err)
	}
}

func TestShortcutsAreEmptyWhereDesktopIsUnsupported(t *testing.T) {
	if ShortcutsSupported() != Supported() {
		t.Fatalf("ShortcutsSupported = %v, Supported = %v — they must agree",
			ShortcutsSupported(), Supported())
	}
	if Supported() {
		t.Skip("this platform has shortcuts; the empty case is elsewhere")
	}
	all, err := Shortcuts()
	if err != nil || len(all) != 0 {
		t.Fatalf("Shortcuts() = %v, %v; want none and no error", all, err)
	}
	if _, err := InstallShortcut(ShortcutSpec{Profile: "work", Dest: DestDesktop, Exe: absExe}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("InstallShortcut = %v; want ErrUnsupported", err)
	}
}
