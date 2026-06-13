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
	s := setupSnippet("zsh", "rig")
	for _, want := range []string{
		markerBegin("rig"),
		"compinit", // cobra's zsh script needs compdef
		`eval "$(command rig completion zsh)"`,
		`rig() {`,
		`__rig_dir="$(command rig "$@")"`,
		`builtin cd -- "$__rig_dir"`,
		`command rig "$@"`,
		markerEnd("rig"),
	} {
		if !strings.Contains(s, want) {
			t.Errorf("zsh snippet missing %q:\n%s", want, s)
		}
	}
	// The base block must NOT carry the rebind line — cobra's own script binds rig.
	if strings.Contains(s, "compdef _rig rig\n") {
		t.Errorf("base zsh snippet should not add a redundant compdef:\n%s", s)
	}
}

func TestSetupSnippet_DevTargetsRigDev(t *testing.T) {
	s := setupSnippet("zsh", "rig-dev")
	for _, want := range []string{
		markerBegin("rig-dev"),                     // its own markers, so it coexists with a rig block
		`eval "$(command rig-dev completion zsh)"`, // completion sourced from the dev binary
		"compdef _rig rig-dev",                     // ...and bound to the rig-dev command
		`rig-dev() {`,                              // dev-named wrapper
		`command rig-dev "$@"`,
		markerEnd("rig-dev"),
	} {
		if !strings.Contains(s, want) {
			t.Errorf("rig-dev zsh snippet missing %q:\n%s", want, s)
		}
	}
	// The --dev block carries the family too, but bound to the -dev launchers so
	// `clauderig-dev <Tab>` completes against the live build.
	for _, want := range []string{
		"Family completions",
		`command -v clauderig-dev >/dev/null 2>&1 && { eval "$(command clauderig-dev completion zsh)"; compdef _clauderig clauderig-dev; }`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("rig-dev zsh block missing %q:\n%s", want, s)
		}
	}
}

func TestSetupSnippet_CanonicalBlockLoadsTheFamily(t *testing.T) {
	// Each shell must source every companion by its own name, guarded so a
	// missing one is skipped.
	cases := map[string][]string{
		"zsh": {
			`command -v clauderig >/dev/null 2>&1 && eval "$(command clauderig completion zsh)"`,
			`command -v relrig >/dev/null 2>&1 && eval "$(command relrig completion zsh)"`,
			`command -v changerig >/dev/null 2>&1 && eval "$(command changerig completion zsh)"`,
		},
		"bash": {`command -v clauderig >/dev/null 2>&1 && eval "$(command clauderig completion bash)"`},
		"fish": {"command -q clauderig; and clauderig completion fish | source"},
		"powershell": {
			"Get-Command -Name clauderig -CommandType Application",
			"& $__bin completion powershell | Out-String | Invoke-Expression",
		},
	}
	for shell, wants := range cases {
		s := setupSnippet(shell, "rig")
		if !strings.Contains(s, "Family completions") {
			t.Errorf("%s canonical block missing the family-completions header:\n%s", shell, s)
		}
		// The canonical block binds the plain names, never the -dev launchers.
		if strings.Contains(s, "clauderig-dev") {
			t.Errorf("%s canonical block should not reference -dev launchers:\n%s", shell, s)
		}
		for _, want := range wants {
			if !strings.Contains(s, want) {
				t.Errorf("%s snippet missing companion line %q:\n%s", shell, want, s)
			}
		}
	}
}

func TestSetupSnippet_DevBlockBindsTheDevCompanions(t *testing.T) {
	// The --dev family lines source the -dev launchers and rebind cobra's
	// completer (named for the base tool) to the -dev command, per shell.
	cases := map[string]string{
		"bash":       `command -v clauderig-dev >/dev/null 2>&1 && { eval "$(command clauderig-dev completion bash)"; complete -o default -o nospace -F __start_clauderig clauderig-dev 2>/dev/null || complete -o default -F __start_clauderig clauderig-dev; }`,
		"fish":       "command -q clauderig-dev; and clauderig-dev completion fish | string replace -a -- '-c clauderig ' '-c clauderig-dev ' | source",
		"powershell": "Register-ArgumentCompleter -CommandName 'clauderig-dev' -ScriptBlock ${__clauderigCompleterBlock}",
	}
	for shell, want := range cases {
		s := setupSnippet(shell, "rig-dev")
		if !strings.Contains(s, want) {
			t.Errorf("%s rig-dev block missing %q:\n%s", shell, want, s)
		}
	}
}

func TestSetupSnippet_BashSkipsTheZshOnlyCompinitGuard(t *testing.T) {
	s := setupSnippet("bash", "rig")
	if !strings.Contains(s, `eval "$(command rig completion bash)"`) {
		t.Error("bash snippet should source cobra's bash completion")
	}
	if strings.Contains(s, "compinit") {
		t.Error("compinit is zsh-only")
	}
	// The dev variant rebinds the bash completion function to rig-dev.
	if dev := setupSnippet("bash", "rig-dev"); !strings.Contains(dev, "__start_rig rig-dev") {
		t.Errorf("rig-dev bash snippet should rebind the completion to rig-dev:\n%s", dev)
	}
}

func TestSetupSnippet_FishUsesFishSyntax(t *testing.T) {
	s := setupSnippet("fish", "rig")
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
	// The dev variant rewrites cobra's `complete -c rig` registration to rig-dev.
	dev := setupSnippet("fish", "rig-dev")
	for _, want := range []string{
		"function rig-dev",
		"string replace -a -- '-c rig ' '-c rig-dev '",
	} {
		if !strings.Contains(dev, want) {
			t.Errorf("rig-dev fish snippet missing %q:\n%s", want, dev)
		}
	}
}

func TestSpliceSnippet_AppendsPreservingExistingContent(t *testing.T) {
	got, changed := spliceSnippet("# my rc\nalias ll='ls -l'\n", setupSnippet("zsh", "rig"), "rig")
	if !changed {
		t.Fatal("a first install should change the file")
	}
	if !strings.HasPrefix(got, "# my rc\nalias ll='ls -l'\n") {
		t.Errorf("existing content must be preserved:\n%s", got)
	}
	if !strings.Contains(got, markerBegin("rig")) || !strings.HasSuffix(got, markerEnd("rig")+"\n") {
		t.Errorf("the snippet should be appended as a marked block:\n%s", got)
	}
}

func TestSpliceSnippet_IsIdempotent(t *testing.T) {
	once, _ := spliceSnippet("", setupSnippet("zsh", "rig"), "rig")
	again, changed := spliceSnippet(once, setupSnippet("zsh", "rig"), "rig")
	if changed {
		t.Fatalf("re-splicing an up-to-date block should be a no-op, got:\n%s", again)
	}
}

func TestSpliceSnippet_ReplacesAStaleBlockInPlace(t *testing.T) {
	stale := "before\n" + markerBegin("rig") + "\nold contents\n" + markerEnd("rig") + "\nafter\n"
	got, changed := spliceSnippet(stale, setupSnippet("bash", "rig"), "rig")
	if !changed {
		t.Fatal("a stale block should be rewritten")
	}
	if strings.Contains(got, "old contents") {
		t.Errorf("the old block should be gone:\n%s", got)
	}
	if !strings.HasPrefix(got, "before\n") || !strings.HasSuffix(got, "\nafter\n") {
		t.Errorf("content around the block must be preserved:\n%s", got)
	}
	if strings.Count(got, markerBegin("rig")) != 1 {
		t.Errorf("exactly one block expected:\n%s", got)
	}
}

// A rig-dev block uses its own markers, so installing it leaves an existing
// rig block intact (they coexist) rather than clobbering it.
func TestSpliceSnippet_DevAndBaseBlocksCoexist(t *testing.T) {
	withRig, _ := spliceSnippet("", setupSnippet("zsh", "rig"), "rig")
	both, changed := spliceSnippet(withRig, setupSnippet("zsh", "rig-dev"), "rig-dev")
	if !changed {
		t.Fatal("installing the rig-dev block into a file with only a rig block should change it")
	}
	if !strings.Contains(both, markerBegin("rig")) || !strings.Contains(both, markerBegin("rig-dev")) {
		t.Errorf("both blocks should be present:\n%s", both)
	}
	if strings.Count(both, "rig() {") != 1 || strings.Count(both, "rig-dev() {") != 1 {
		t.Errorf("each wrapper should appear exactly once:\n%s", both)
	}
	// Re-installing rig-dev is idempotent and still doesn't disturb the rig block.
	again, changed := spliceSnippet(both, setupSnippet("zsh", "rig-dev"), "rig-dev")
	if changed || again != both {
		t.Errorf("re-installing rig-dev should be a no-op")
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
	if !strings.Contains(string(data), markerBegin("rig")) || !strings.Contains(string(data), "rig() {") {
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
	if strings.Count(string(after), markerBegin("rig")) != 1 {
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
	got := setupSnippet("powershell", "rig")
	for _, want := range []string{
		markerBegin("rig"), markerEnd("rig"),
		"completion powershell | Out-String | Invoke-Expression",
		"function rig {",
		"Set-Location -LiteralPath $dir",
		"Get-Command -Name rig -CommandType Application",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("powershell snippet missing %q:\n%s", want, got)
		}
	}
	// The dev variant re-registers cobra's completer block for rig-dev.
	if dev := setupSnippet("powershell", "rig-dev"); !strings.Contains(dev, "Register-ArgumentCompleter -CommandName 'rig-dev' -ScriptBlock ${__rigCompleterBlock}") {
		t.Errorf("rig-dev powershell snippet should re-register the completer:\n%s", dev)
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
	changed, err := installSnippet(rc, setupSnippet("powershell", "rig"), "rig")
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
