//go:build darwin

package desktop

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeClauderig writes an executable that records the arguments it was called
// with, standing in for the real binary so a bundle can be RUN rather than only
// inspected. The directory it lives in carries a space and a single quote —
// both of which break a naively built shell script.
func fakeClauderig(t *testing.T) (exe, record string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "bin dir's")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	record = filepath.Join(dir, "args.txt")
	exe = filepath.Join(dir, "clauderig")
	body := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + shQuote(record) + "\n"
	if err := os.WriteFile(exe, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return exe, record
}

// The whole point of the bundle is that double-clicking it runs
// `clauderig desktop open <profile>`. Inspecting the script's text would pass
// while the script itself failed to parse, so this executes it.
func TestBundleRunsDesktopOpen(t *testing.T) {
	dir := t.TempDir()
	exe, record := fakeClauderig(t)

	sc, err := installShortcutIn(dir, ShortcutSpec{
		Profile: "work_1", Label: "Claude - work", Dest: DestDesktop, Exe: exe,
	})
	if err != nil {
		t.Fatal(err)
	}
	if sc.Path != filepath.Join(dir, "Claude - work.app") {
		t.Fatalf("bundle at %q", sc.Path)
	}

	launcher := filepath.Join(sc.Path, "Contents", "MacOS", bundleExec)
	fi, err := os.Stat(launcher)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&0o111 == 0 {
		t.Fatalf("launcher mode = %v, want executable", fi.Mode().Perm())
	}
	if out, rerr := exec.Command(launcher).CombinedOutput(); rerr != nil {
		t.Fatalf("running the bundle: %v: %s", rerr, out)
	}
	got := strings.Split(strings.TrimSpace(readFile(t, record)), "\n")
	want := []string{"desktop", "open", "work_1"}
	if len(got) != len(want) {
		t.Fatalf("clauderig called with %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("clauderig called with %q, want %q", got, want)
		}
	}
}

// A bundle that cannot find clauderig must fail LOUDLY: a GUI launch has
// nowhere to print, so a silent exit leaves the user clicking a dead icon.
//
// The failure branch is checked without RUNNING it. Running it calls osascript,
// which puts a modal alert on the screen of whoever is running the tests and
// waits for them to click OK — which is correct behaviour for the shortcut and
// intolerable in a test suite. What can be checked without a display is that
// the script parses (`sh -n` covers every branch, not just the one that runs),
// that the alert is reached before anything else when the binary is missing,
// and that the message names the path that went away.
func TestBundleAlertsWhenClauderigIsGone(t *testing.T) {
	dir := t.TempDir()
	exe, _ := fakeClauderig(t)
	sc, err := installShortcutIn(dir, ShortcutSpec{Profile: "work", Dest: DestDesktop, Exe: exe})
	if err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(sc.Path, "Contents", "MacOS", bundleExec)
	if out, serr := exec.Command("/bin/sh", "-n", launcher).CombinedOutput(); serr != nil {
		t.Fatalf("the launcher is not valid sh: %v: %s", serr, out)
	}
	body := readFile(t, launcher)
	for _, want := range []string{
		`if [ ! -x "$CLAUDERIG" ]; then`,
		"alert ",
		"osascript",
		"$CLAUDERIG, which is missing",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("launcher is missing %q:\n%s", want, body)
		}
	}
	// The path is interpolated by the shell at click time rather than baked in,
	// so an exe with a quote in it cannot break the message.
	if !strings.Contains(body, "CLAUDERIG="+shQuote(exe)) {
		t.Fatalf("launcher does not quote the binary path:\n%s", body)
	}
}

func TestBundleIsFoundAndRemovedByProfile(t *testing.T) {
	dir := t.TempDir()
	exe, _ := fakeClauderig(t)
	if _, err := installShortcutIn(dir, ShortcutSpec{Profile: "work", Dest: DestDesktop, Exe: exe}); err != nil {
		t.Fatal(err)
	}
	// A neighbouring app that is not ours, and a bundle-shaped directory with
	// no marker: neither may show up in a listing that `rm` deletes from.
	if err := os.MkdirAll(filepath.Join(dir, "Some Other.app", "Contents", "MacOS"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "notes.txt"), "hello")

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
		t.Fatalf("bundle still there: %v", serr)
	}
	if _, serr := os.Stat(filepath.Join(dir, "Some Other.app")); serr != nil {
		t.Fatalf("removing our shortcut disturbed a neighbour: %v", serr)
	}
}

// Re-running the command is how a shortcut is repaired after clauderig moves,
// so replacing one of ours must be ordinary — while a file someone else put
// there is refused until --force.
func TestBundleReplacesOnlyItsOwn(t *testing.T) {
	dir := t.TempDir()
	exe, _ := fakeClauderig(t)
	spec := ShortcutSpec{Profile: "work", Dest: DestDesktop, Exe: exe}
	first, err := installShortcutIn(dir, spec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := installShortcutIn(dir, spec); err != nil {
		t.Fatalf("re-running over our own shortcut: %v", err)
	}
	if found, lerr := listShortcutsIn(dir); lerr != nil || len(found) != 1 {
		t.Fatalf("after a rewrite: %+v, %v — want exactly one", found, lerr)
	}

	// Someone else's bundle at the same path.
	if err := os.RemoveAll(first.Path); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(first.Path, "Contents", "Info.plist"), "not ours")
	if _, err := installShortcutIn(dir, spec); !errors.Is(err, ErrShortcutExists) {
		t.Fatalf("installing over a foreign bundle = %v, want ErrShortcutExists", err)
	}
	spec.Force = true
	if _, err := installShortcutIn(dir, spec); err != nil {
		t.Fatalf("--force over a foreign bundle: %v", err)
	}
	if _, ok := bundleProfile(first.Path); !ok {
		t.Fatal("--force did not leave one of our bundles behind")
	}
}

// A failed build must not take the working shortcut with it: the bundle is
// staged elsewhere and swapped in, so the old one survives.
func TestBundleSurvivesAFailedRewrite(t *testing.T) {
	dir := t.TempDir()
	exe, _ := fakeClauderig(t)
	spec := ShortcutSpec{Profile: "work", Dest: DestDesktop, Exe: exe}
	sc, err := installShortcutIn(dir, spec)
	if err != nil {
		t.Fatal(err)
	}
	// A read-only destination fails the staging write, which is as close to a
	// mid-build failure as a test can get without instrumenting the writer.
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	if _, err := installShortcutIn(dir, spec); err == nil {
		t.Skip("this filesystem let the write through; nothing to assert")
	}
	if _, ok := bundleProfile(sc.Path); !ok {
		t.Fatal("the previous shortcut was destroyed by a failed rewrite")
	}
}

// The label reaches Info.plist as XML, and a --label is whatever the user typed.
func TestBundlePlistEscapesTheLabel(t *testing.T) {
	dir := t.TempDir()
	exe, _ := fakeClauderig(t)
	sc, err := installShortcutIn(dir, ShortcutSpec{
		Profile: "work", Label: "Claude & Co", Dest: DestDesktop, Exe: exe,
	})
	if err != nil {
		t.Fatal(err)
	}
	plist := readFile(t, filepath.Join(sc.Path, "Contents", "Info.plist"))
	if !strings.Contains(plist, "<string>Claude &amp; Co</string>") {
		t.Fatalf("label not escaped in Info.plist:\n%s", plist)
	}
	if strings.Contains(plist, "Claude & Co") {
		t.Fatalf("raw ampersand left in Info.plist:\n%s", plist)
	}
}

func TestShortcutDirFollowsHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for dest, want := range map[Dest]string{
		DestDesktop: filepath.Join(home, "Desktop"),
		DestApps:    filepath.Join(home, "Applications"),
	} {
		got, err := shortcutDir(dest)
		if err != nil || got != want {
			t.Fatalf("shortcutDir(%s) = %q, %v; want %q", dest, got, err, want)
		}
	}
}

// A directory nobody has created yet is not an error — it is an empty desktop.
func TestListShortcutsInMissingDirIsEmpty(t *testing.T) {
	found, err := listShortcutsIn(filepath.Join(t.TempDir(), "nope"))
	if err != nil || len(found) != 0 {
		t.Fatalf("listShortcutsIn(missing) = %+v, %v", found, err)
	}
}
