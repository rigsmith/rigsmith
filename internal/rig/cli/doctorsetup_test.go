package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// zshHome points the shell-state readers at a temp rc file, on every OS: a
// $SHELL rig recognizes wins over the Windows PowerShell default, and $ZDOTDIR
// is where rcFileFor looks for .zshrc.
func zshHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("SHELL", "/bin/zsh")
	t.Setenv("ZDOTDIR", dir)
	return filepath.Join(dir, ".zshrc")
}

func writeRC(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The two features the setup block provides — `rig cd` and completion — fail
// silently when it isn't there, which is why it gets asked about. Say so, and
// name the command that fixes it.
func TestShellCheckWarnsWhenTheBlockIsMissing(t *testing.T) {
	zshHome(t)

	c := shellIntegrationCheck(readShellState())

	if c.level != docWarn {
		t.Errorf("level = %v, want a warning for a missing block", c.level)
	}
	for _, want := range []string{"rig cd", "rig setup zsh"} {
		if !strings.Contains(c.detail, want) {
			t.Errorf("detail missing %q; got: %s", want, c.detail)
		}
	}
}

// The block rig would write right now is the definition of current.
func TestShellCheckPassesOnTheCurrentBlock(t *testing.T) {
	rc := zshHome(t)
	writeRC(t, rc, setupSnippet("zsh", integrationBase))

	c := shellIntegrationCheck(readShellState())

	if c.level != docOK {
		t.Fatalf("level = %v (%s), want ok for the block rig writes today", c.level, c.detail)
	}
	if !strings.Contains(c.detail, rc) {
		t.Errorf("detail should name the rc file; got: %s", c.detail)
	}
}

// A block from an older rig still works well enough to hide the fact that it's
// stale — a companion added since won't be wired, for instance — so it warns.
func TestShellCheckWarnsOnADifferingBlock(t *testing.T) {
	rc := zshHome(t)
	writeRC(t, rc, markerBegin(integrationBase)+"\n# from another rig\n"+markerEnd(integrationBase))

	c := shellIntegrationCheck(readShellState())

	if c.level != docWarn || !strings.Contains(c.detail, "differs") {
		t.Errorf("want a differing-block warning, got %v: %s", c.level, c.detail)
	}
	// A byte difference proves neither age nor authorship — the block may come
	// from a NEWER rig — so the message must not claim it is older. (Compared
	// with the rc path removed: temp paths under /var/folders contain "older".)
	if strings.Contains(strings.ReplaceAll(c.detail, rc, ""), "older") {
		t.Errorf("the warning still claims the block is older: %s", c.detail)
	}
}

// Having only a --dev block is the state behind "I ran setup and `rig cd`
// still doesn't work": what got installed binds rig-dev, not rig.
func TestShellCheckCallsOutADevOnlyBlock(t *testing.T) {
	rc := zshHome(t)
	dev := integrationBase + "-dev"
	writeRC(t, rc, markerBegin(dev)+"\n# dev launcher\n"+markerEnd(dev))

	c := shellIntegrationCheck(readShellState())

	if c.level != docWarn || !strings.Contains(c.detail, dev) {
		t.Errorf("want the dev-only block named, got %v: %s", c.level, c.detail)
	}
}

// A shell rig has no snippet for is a fact about the shell, not a defect in the
// setup — and the aliases row must not claim "none installed" off the back of
// a file it never read.
func TestUnsupportedShellIsReportedNotFaulted(t *testing.T) {
	t.Setenv("SHELL", "/bin/ksh")
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no unsupported-shell case: it defaults to powershell")
	}
	state := readShellState()

	shell := shellIntegrationCheck(state)
	if shell.level != docOK || !strings.Contains(shell.detail, "ksh") {
		t.Errorf("want an ok row naming the shell, got %v: %s", shell.level, shell.detail)
	}
	aliases := aliasCheck(state)
	if aliases.level != docOK || !strings.Contains(aliases.detail, "not checked") {
		t.Errorf("aliases should report that it couldn't check, got %v: %s", aliases.level, aliases.detail)
	}
}

// Aliases are opt-in: none installed is a choice, so it reports without
// faulting — but a partial install is named, since the way people find out `rt`
// was never installed is by typing it.
func TestAliasCheckReportsWhatIsLive(t *testing.T) {
	rc := zshHome(t)
	writeRC(t, rc, aliasSnippetFor("zsh", rigAliases[:2]))

	c := aliasCheck(readShellState())

	if c.level != docOK {
		t.Fatalf("level = %v, want ok — aliases are opt-in", c.level)
	}
	want := aliasNamesOf(rigAliases[:2])
	if !strings.Contains(c.detail, want) || !strings.Contains(c.detail, "of") {
		t.Errorf("want a partial-install detail naming %q; got: %s", want, c.detail)
	}
}

func TestAliasCheckReportsNoneInstalled(t *testing.T) {
	zshHome(t)

	if c := aliasCheck(readShellState()); c.level != docOK || !strings.Contains(c.detail, "rig alias install") {
		t.Errorf("want an ok row pointing at the installer, got %v: %s", c.level, c.detail)
	}
}

// fakeRig writes an executable named like rig into dir and returns the path as
// the checks report it — symlinks resolved, which on Windows also expands the
// 8.3 short form t.TempDir() hands back.
func fakeRig(t *testing.T, dir string) string {
	t.Helper()
	name := integrationBase
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

// Two rigs on PATH is the failure that hides itself: the first one answers, and
// it may not be the one you just upgraded.
func TestRigCheckWarnsOnTwoCopiesOnPath(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	a, b := fakeRig(t, first), fakeRig(t, second)
	t.Setenv("PATH", first+string(os.PathListSeparator)+second)

	c := rigBinaryCheck()

	if c.level != docWarn {
		t.Fatalf("level = %v (%s), want a warning for two copies", c.level, c.detail)
	}
	if !strings.Contains(c.detail, a) || !strings.Contains(c.detail, b) {
		t.Errorf("both copies should be named; got: %s", c.detail)
	}
}

// One copy, and it isn't this binary: a source build or a -dev launcher. That
// is a normal dev loop, so it reports what typing `rig` would run instead of
// crying wolf.
func TestRigCheckDoesNotFaultADevBuild(t *testing.T) {
	dir := t.TempDir()
	installed := fakeRig(t, dir)
	t.Setenv("PATH", dir)

	c := rigBinaryCheck()

	if c.level != docOK {
		t.Fatalf("level = %v (%s), want ok — running a build by path is not a defect", c.level, c.detail)
	}
	if !strings.Contains(c.detail, installed) {
		t.Errorf("want the PATH copy named; got: %s", c.detail)
	}
}

// With no rig on PATH at all there is nothing to compare against, and nothing
// to warn about.
func TestRigCheckWithNothingOnPath(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	c := rigBinaryCheck()

	if c.level != docOK || !strings.Contains(c.detail, "not on your PATH") {
		t.Errorf("want an ok row saying it isn't on PATH, got %v: %s", c.level, c.detail)
	}
}

// The group exists at all, with the four rows, whatever the machine looks like.
func TestSetupChecksAlwaysProduceTheGroup(t *testing.T) {
	zshHome(t)

	var labels []string
	for _, pc := range setupChecks() {
		if pc.eco != setupGroup {
			t.Errorf("%s: eco = %q, want %q", pc.label, pc.eco, setupGroup)
		}
		labels = append(labels, pc.label)
		pc.run() // must not panic on any machine state
	}
	if got := strings.Join(labels, ","); got != "rig,family,shell,aliases" {
		t.Errorf("rows = %s, want rig,family,shell,aliases", got)
	}
}

// `rig alias list` marks what's live, so it answers "why doesn't rt work"
// rather than only "what could I install".
func TestAliasListMarksInstalledAliases(t *testing.T) {
	rc := zshHome(t)
	installed, absent := rigAliases[0], rigAliases[1]
	writeRC(t, rc, aliasSnippetFor("zsh", []rigAlias{installed}))

	out, err := runSub(t, newAliasListCmd())
	if err != nil {
		t.Fatal(err)
	}

	if line := aliasRow(out, installed.name); !strings.HasPrefix(strings.TrimSpace(line), aliasInstalledMark) {
		t.Errorf("%s is installed and should be marked; got: %q", installed.name, line)
	}
	if line := aliasRow(out, absent.name); strings.HasPrefix(strings.TrimSpace(line), aliasInstalledMark) {
		t.Errorf("%s is not installed and should not be marked; got: %q", absent.name, line)
	}
	if !strings.Contains(out, rc) {
		t.Errorf("the legend should name the rc file; got:\n%s", out)
	}
}

// aliasRow finds the listing row for one alias by its name column.
func aliasRow(out, name string) string {
	for _, line := range strings.Split(out, "\n") {
		for _, field := range strings.Fields(line) {
			if field == name {
				return line
			}
		}
	}
	return ""
}

// A shell rig can't read means the marks prove nothing, and the legend has to
// say so instead of reading as "none installed".
func TestAliasListSaysWhenItCannotTell(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows defaults to powershell, so there is no unreadable-shell case")
	}
	t.Setenv("SHELL", "/bin/ksh")

	out, err := runSub(t, newAliasListCmd())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "couldn't tell") {
		t.Errorf("want the legend to admit it couldn't check; got:\n%s", out)
	}
}
