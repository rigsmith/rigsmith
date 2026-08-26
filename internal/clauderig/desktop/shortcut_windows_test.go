//go:build windows

package desktop

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readLnk reads back the fields the shell actually stored, so the tests assert
// what Windows will run rather than what the script was told to write.
func readLnk(t *testing.T, path string) (target, args, desc string) {
	t.Helper()
	const script = `$ErrorActionPreference='Stop'
$s = (New-Object -ComObject WScript.Shell).CreateShortcut($env:CLAUDERIG_SC_PATH)
ConvertTo-Json -Compress -InputObject @{ Target = $s.TargetPath; Args = $s.Arguments; Desc = $s.Description }`
	out, err := runPowerShell(script, map[string]string{"CLAUDERIG_SC_PATH": path})
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var row struct{ Target, Args, Desc string }
	if uerr := json.Unmarshal([]byte(strings.TrimSpace(out)), &row); uerr != nil {
		t.Fatalf("parse %s: %v (%s)", path, uerr, out)
	}
	return row.Target, row.Args, row.Desc
}

// The shortcut has to run `clauderig desktop open <profile>` — that is the whole
// contract, and it lives in fields only the shell can read back.
func TestLnkRunsDesktopOpen(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "clauderig.exe")
	if err := os.WriteFile(exe, []byte("stub"), 0o644); err != nil {
		t.Fatal(err)
	}

	sc, err := installShortcutIn(dir, ShortcutSpec{
		Profile: "work_1", Label: "Claude - work", Dest: DestDesktop, Exe: exe,
	})
	if err != nil {
		t.Fatal(err)
	}
	if sc.Path != filepath.Join(dir, "Claude - work.lnk") {
		t.Fatalf("shortcut at %q", sc.Path)
	}
	target, args, desc := readLnk(t, sc.Path)
	if !strings.EqualFold(target, exe) {
		t.Fatalf("target = %q, want %q", target, exe)
	}
	if args != `desktop open "work_1"` {
		t.Fatalf("arguments = %q", args)
	}
	if got, ok := profileFromTag(desc); !ok || got != "work_1" {
		t.Fatalf("description %q does not identify the profile", desc)
	}
}

func TestLnkIsFoundAndRemovedByProfile(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "clauderig.exe")
	if err := os.WriteFile(exe, []byte("stub"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installShortcutIn(dir, ShortcutSpec{Profile: "work", Dest: DestDesktop, Exe: exe}); err != nil {
		t.Fatal(err)
	}
	// Somebody else's shortcut in the same folder must never be listed — the
	// listing is what `desktop rm` deletes from.
	const script = `$ErrorActionPreference='Stop'
$s = (New-Object -ComObject WScript.Shell).CreateShortcut((Join-Path $env:CLAUDERIG_SC_DIR 'Notepad.lnk'))
$s.TargetPath = 'C:\Windows\notepad.exe'
$s.Description = 'nothing to do with clauderig'
$s.Save()`
	if _, err := runPowerShell(script, map[string]string{"CLAUDERIG_SC_DIR": dir}); err != nil {
		t.Fatal(err)
	}

	found, err := listShortcutsIn(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].Profile != "work" || found[0].Label != "Claude - work" {
		t.Fatalf("listShortcutsIn = %+v, want one work shortcut", found)
	}
	if err := removeShortcutAt(found[0].Path); err != nil {
		t.Fatal(err)
	}
	if _, serr := os.Stat(found[0].Path); !errors.Is(serr, os.ErrNotExist) {
		t.Fatalf("shortcut still there: %v", serr)
	}
	if _, serr := os.Stat(filepath.Join(dir, "Notepad.lnk")); serr != nil {
		t.Fatalf("removing our shortcut disturbed a neighbour: %v", serr)
	}
}

// Re-running repairs a shortcut after clauderig moves; a file someone else put
// there is refused until --force.
func TestLnkReplacesOnlyItsOwn(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "clauderig.exe")
	if err := os.WriteFile(exe, []byte("stub"), 0o644); err != nil {
		t.Fatal(err)
	}
	spec := ShortcutSpec{Profile: "work", Dest: DestDesktop, Exe: exe}
	sc, err := installShortcutIn(dir, spec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := installShortcutIn(dir, spec); err != nil {
		t.Fatalf("re-running over our own shortcut: %v", err)
	}

	// A shortcut of the same name that clauderig did not write.
	if err := os.Remove(sc.Path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sc.Path, []byte("not a real shortcut"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installShortcutIn(dir, spec); !errors.Is(err, ErrShortcutExists) {
		t.Fatalf("installing over a foreign file = %v, want ErrShortcutExists", err)
	}
	spec.Force = true
	if _, err := installShortcutIn(dir, spec); err != nil {
		t.Fatalf("--force over a foreign file: %v", err)
	}
	found, lerr := listShortcutsIn(dir)
	if lerr != nil || len(found) != 1 || found[0].Profile != "work" {
		t.Fatalf("after --force: %+v, %v", found, lerr)
	}
}

// The destination comes from Windows rather than from %USERPROFILE%\Desktop, so
// a OneDrive-redirected Desktop is still the one the user sees.
func TestShortcutDirAsksWindows(t *testing.T) {
	for _, d := range AllDests() {
		dir, err := shortcutDir(d)
		if err != nil {
			t.Fatalf("shortcutDir(%s): %v", d, err)
		}
		if !filepath.IsAbs(dir) {
			t.Fatalf("shortcutDir(%s) = %q, want an absolute path", d, dir)
		}
	}
}

func TestListShortcutsInMissingDirIsEmpty(t *testing.T) {
	found, err := listShortcutsIn(filepath.Join(t.TempDir(), "nope"))
	if err != nil || len(found) != 0 {
		t.Fatalf("listShortcutsIn(missing) = %+v, %v", found, err)
	}
}
