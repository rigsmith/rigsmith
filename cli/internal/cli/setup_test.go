// Tests for `rig setup` — the rc-file shell integration installer. All paths
// are hermetic: HOME (and the shell-specific overrides) point at temp dirs,
// so the developer's real rc files are never touched.
package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeHome points HOME at a temp dir (os.UserHomeDir reads it on Unix) and
// clears the shell-specific location overrides.
func fakeHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir reads this on Windows
	t.Setenv("ZDOTDIR", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("RIG_PWSH_PROFILE", "") // never query a real pwsh in tests
	return home
}

func TestSetupSnippet_ZshCarriesCompletionAndTheCdWrapper(t *testing.T) {
	s := setupSnippet("zsh")
	for _, want := range []string{
		setupBegin,
		"compinit", // cobra's zsh script needs compdef
		`eval "$(command rig completion zsh)"`,
		`rig() {`,
		`__rig_dir="$(command rig "$@")"`,
		`builtin cd -- "$__rig_dir"`,
		`command rig "$@"`,
		setupEnd,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("zsh snippet missing %q:\n%s", want, s)
		}
	}
}

func TestSetupSnippet_BashSkipsTheZshOnlyCompinitGuard(t *testing.T) {
	s := setupSnippet("bash")
	if !strings.Contains(s, `eval "$(command rig completion bash)"`) {
		t.Error("bash snippet should source cobra's bash completion")
	}
	if strings.Contains(s, "compinit") {
		t.Error("compinit is zsh-only")
	}
}

func TestSetupSnippet_FishUsesFishSyntax(t *testing.T) {
	s := setupSnippet("fish")
	for _, want := range []string{
		"command rig completion fish | source",
		"function rig",
		"set -l __rig_dir (command rig $argv)",
		"builtin cd -- $__rig_dir",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("fish snippet missing %q:\n%s", want, s)
		}
	}
}

func TestSpliceSnippet_AppendsPreservingExistingContent(t *testing.T) {
	got, changed := spliceSnippet("# my rc\nalias ll='ls -l'\n", setupSnippet("zsh"))
	if !changed {
		t.Fatal("a first install should change the file")
	}
	if !strings.HasPrefix(got, "# my rc\nalias ll='ls -l'\n") {
		t.Errorf("existing content must be preserved:\n%s", got)
	}
	if !strings.Contains(got, setupBegin) || !strings.HasSuffix(got, setupEnd+"\n") {
		t.Errorf("the snippet should be appended as a marked block:\n%s", got)
	}
}

func TestSpliceSnippet_IsIdempotent(t *testing.T) {
	once, _ := spliceSnippet("", setupSnippet("zsh"))
	again, changed := spliceSnippet(once, setupSnippet("zsh"))
	if changed {
		t.Fatalf("re-splicing an up-to-date block should be a no-op, got:\n%s", again)
	}
}

func TestSpliceSnippet_ReplacesAStaleBlockInPlace(t *testing.T) {
	stale := "before\n" + setupBegin + "\nold contents\n" + setupEnd + "\nafter\n"
	got, changed := spliceSnippet(stale, setupSnippet("bash"))
	if !changed {
		t.Fatal("a stale block should be rewritten")
	}
	if strings.Contains(got, "old contents") {
		t.Errorf("the old block should be gone:\n%s", got)
	}
	if !strings.HasPrefix(got, "before\n") || !strings.HasSuffix(got, "\nafter\n") {
		t.Errorf("content around the block must be preserved:\n%s", got)
	}
	if strings.Count(got, setupBegin) != 1 {
		t.Errorf("exactly one block expected:\n%s", got)
	}
}

func TestRcFileFor_DefaultsAndOverrides(t *testing.T) {
	home := fakeHome(t)

	if got, _ := rcFileFor("zsh"); got != filepath.Join(home, ".zshrc") {
		t.Errorf("zsh rc = %q, want ~/.zshrc", got)
	}
	if got, _ := rcFileFor("bash"); got != filepath.Join(home, ".bashrc") {
		t.Errorf("bash rc = %q, want ~/.bashrc", got)
	}
	if got, _ := rcFileFor("fish"); got != filepath.Join(home, ".config", "fish", "config.fish") {
		t.Errorf("fish rc = %q, want ~/.config/fish/config.fish", got)
	}

	zdot := t.TempDir()
	t.Setenv("ZDOTDIR", zdot)
	if got, _ := rcFileFor("zsh"); got != filepath.Join(zdot, ".zshrc") {
		t.Errorf("zsh rc = %q, want $ZDOTDIR/.zshrc", got)
	}
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	if got, _ := rcFileFor("fish"); got != filepath.Join(xdg, "fish", "config.fish") {
		t.Errorf("fish rc = %q, want $XDG_CONFIG_HOME/fish/config.fish", got)
	}
}

func TestSetupCommand_InstallsOnceAndIsIdempotent(t *testing.T) {
	home := fakeHome(t)
	rc := filepath.Join(home, ".zshrc")

	cmd := newSetupCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"zsh"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("setup: %v", err)
	}
	data, err := os.ReadFile(rc)
	if err != nil {
		t.Fatalf("the rc file should exist: %v", err)
	}
	if !strings.Contains(string(data), setupBegin) || !strings.Contains(string(data), "rig() {") {
		t.Fatalf("rc file missing the integration block:\n%s", data)
	}

	// Second run: no duplicate block, a friendly notice instead.
	buf.Reset()
	cmd2 := newSetupCmd()
	cmd2.SetOut(&buf)
	cmd2.SetErr(&buf)
	cmd2.SetArgs([]string{"zsh"})
	if err := cmd2.Execute(); err != nil {
		t.Fatalf("re-setup: %v", err)
	}
	if !strings.Contains(buf.String(), "already installed") {
		t.Fatalf("output = %q, want the already-installed notice", buf.String())
	}
	after, _ := os.ReadFile(rc)
	if string(after) != string(data) {
		t.Fatal("a re-run must leave the rc file byte-identical")
	}
	if strings.Count(string(after), setupBegin) != 1 {
		t.Fatal("the block must not duplicate on re-run")
	}
}

func TestSetupCommand_PrintWritesNothing(t *testing.T) {
	home := fakeHome(t)

	cmd := newSetupCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"fish", "--print"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("setup --print: %v", err)
	}
	if !strings.Contains(buf.String(), "command rig completion fish | source") {
		t.Fatalf("output = %q, want the fish snippet", buf.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "fish", "config.fish")); !os.IsNotExist(err) {
		t.Fatal("--print must not write the rc file")
	}
}

func TestSetupCommand_RejectsAnUnknownShell(t *testing.T) {
	fakeHome(t)
	t.Setenv("SHELL", "/bin/tcsh")

	cmd := newSetupCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs(nil)
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), `unknown shell "tcsh"`) {
		t.Fatalf("err = %v, want the unknown-shell message", err)
	}
}

func TestSetupPowershellSnippet(t *testing.T) {
	got := setupSnippet("powershell")
	for _, want := range []string{
		setupBegin, setupEnd,
		"completion powershell | Out-String | Invoke-Expression",
		"function rig {",
		"Set-Location -LiteralPath $dir",
		"Get-Command -Name rig -CommandType Application",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("powershell snippet missing %q:\n%s", want, got)
		}
	}
}

func TestSetupPowershellProfileSeamAndInstall(t *testing.T) {
	home := fakeHome(t)
	profile := filepath.Join(home, "pwsh", "profile.ps1")
	t.Setenv("RIG_PWSH_PROFILE", profile)

	rc, err := rcFileFor("powershell")
	if err != nil || rc != profile {
		t.Fatalf("rcFileFor(powershell) = %q, %v; want the RIG_PWSH_PROFILE seam", rc, err)
	}
	changed, err := installSnippet(rc, setupSnippet("powershell"))
	if err != nil || !changed {
		t.Fatalf("install = %v, %v", changed, err)
	}
	data, err := os.ReadFile(profile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "function rig {") {
		t.Errorf("profile missing the wrapper:\n%s", data)
	}
	// pwsh alias normalizes (exercised at the command layer elsewhere); the
	// fallback path stays under the test home when no seam and no pwsh on PATH.
	t.Setenv("RIG_PWSH_PROFILE", "")
	t.Setenv("PATH", home) // hide any real pwsh/powershell
	rc, err = rcFileFor("powershell")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(rc, home) {
		t.Errorf("fallback profile %q escaped the test home", rc)
	}
}
