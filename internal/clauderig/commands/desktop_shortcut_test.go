package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/internal/clauderig/desktop"
)

func TestParseDests(t *testing.T) {
	got, err := parseDests([]string{"desktop", "apps"})
	if err != nil || len(got) != 2 || got[0] != desktop.DestDesktop || got[1] != desktop.DestApps {
		t.Fatalf("parseDests = %v, %v", got, err)
	}
	// `--to desktop --to desktop` asks for one shortcut, not two writes to the
	// same path — the second of which would report replacing the first.
	if got, err := parseDests([]string{"desktop", "Desktop"}); err != nil || len(got) != 1 {
		t.Fatalf("parseDests(duplicate) = %v, %v", got, err)
	}
	if _, err := parseDests([]string{"dock"}); err == nil {
		t.Fatal("parseDests(dock) should have failed")
	}
	if _, err := parseDests(nil); err == nil {
		t.Fatal("parseDests(nothing) should have failed")
	}
}

func TestShortcutTargetsAll(t *testing.T) {
	st := targetStore(t) // work, personal
	all, err := shortcutTargets(st, stubApp{}, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("shortcutTargets(--all) = %+v, want both profiles", all)
	}
	// --all and a name are contradictory instructions, and silently honouring
	// one of them would write the wrong number of shortcuts.
	if _, err := shortcutTargets(st, stubApp{}, []string{"work"}, true); err == nil {
		t.Fatal("--all with a named profile should have failed")
	}
	empty := desktop.NewStore(filepath.Join(t.TempDir(), "desktop"))
	if _, err := shortcutTargets(empty, stubApp{}, nil, true); err == nil {
		t.Fatal("--all with no profiles should have failed")
	}
}

func TestShortcutTargetsNamed(t *testing.T) {
	st := targetStore(t)
	got, err := shortcutTargets(st, stubApp{}, []string{"personal"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "personal" {
		t.Fatalf("shortcutTargets = %+v, want personal", got)
	}
}

// A shortcut records an absolute path to clauderig, so one written by a `go run`
// build would point into a directory that is deleted minutes later — and fail
// silently, at click time, long after anyone connects the two.
func TestShortcutExeRefusesATemporaryBuild(t *testing.T) {
	tmpBin := filepath.Join(t.TempDir(), "clauderig")
	if err := os.WriteFile(tmpBin, []byte("stub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, is := underTempDir(tmpBin); !is {
		t.Skip("t.TempDir() is not under the system temp directory here")
	}
	// The running test binary IS such a build, so the default path must refuse.
	if _, err := shortcutExe(""); err == nil {
		t.Fatal("shortcutExe() accepted a test binary in the temp directory")
	} else if !strings.Contains(err.Error(), "--exe") {
		t.Fatalf("shortcutExe() = %v; want the --exe way out to be named", err)
	}
	// …and --exe is that way out, so it must not apply the same rule.
	if got, err := shortcutExe(tmpBin); err != nil || got != tmpBin {
		t.Fatalf("shortcutExe(--exe tmp) = %q, %v; want it honoured", got, err)
	}
}

func TestShortcutExeChecksTheOverride(t *testing.T) {
	dir := t.TempDir()
	if _, err := shortcutExe(dir); err == nil {
		t.Fatal("--exe pointing at a directory should have failed")
	}
	if _, err := shortcutExe(filepath.Join(dir, "nope")); err == nil {
		t.Fatal("--exe pointing at nothing should have failed")
	}
	// A relative --exe is made absolute rather than refused: a shortcut needs an
	// absolute path, and the shell the user typed it in has a working directory.
	bin := filepath.Join(dir, "clauderig")
	if err := os.WriteFile(bin, []byte("stub"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	got, err := shortcutExe("clauderig")
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(got) {
		t.Fatalf("shortcutExe(relative) = %q, want an absolute path", got)
	}
}

func TestUnderTempDir(t *testing.T) {
	if _, is := underTempDir(filepath.Join(t.TempDir(), "x")); !is {
		t.Fatal("a path inside the temp directory was not recognised")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	// A near-miss: a sibling whose path merely starts with the same characters.
	if _, is := underTempDir(filepath.Clean(os.TempDir()) + "-elsewhere/clauderig"); is {
		t.Fatal("a sibling of the temp directory was mistaken for being inside it")
	}
	if _, is := underTempDir(filepath.Join(home, "go", "bin", "clauderig")); is {
		t.Fatal("an installed binary was mistaken for a temporary build")
	}
}
