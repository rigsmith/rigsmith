// Tests for `rig alias` — the short-verb shell aliases installer. Like the
// setup tests, every path is hermetic: HOME points at a temp dir (via
// fakeHome, defined in setup_test.go), so real rc files are never touched.
package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAliasSnippet_PosixUsesAliasBuiltin(t *testing.T) {
	s := aliasSnippet("zsh")
	for _, want := range []string{
		aliasMarkerBegin(),
		"alias rr='rig run'",
		"alias ri='rig install'",
		"alias rup='rig upgrade'",
		"alias rrm='rig uninstall'",
		aliasMarkerEnd(),
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("zsh snippet missing %q:\n%s", want, s)
		}
	}
	// bash shares the POSIX rendering.
	if aliasSnippet("bash") != s {
		t.Fatal("bash and zsh alias snippets should be identical")
	}
}

func TestAliasSnippet_IncludesInnerLoopVerbs(t *testing.T) {
	s := aliasSnippet("zsh")
	for _, want := range []string{
		"alias rb='rig build'",
		"alias rt='rig test'",
		"alias rf='rig format'",
		"alias rl='rig lint'",
		"alias rk='rig kill'",
		"alias rw='rig watch'",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("zsh snippet missing %q:\n%s", want, s)
		}
	}
}

func TestAliasSnippet_RcdIsSelfContainedCdFunction(t *testing.T) {
	// rcd must be a function that captures `rig cd`'s printed dir and cds — a
	// plain alias would only print. It calls the binary directly (command rig),
	// so it works with or without the setup wrapper.
	z := aliasSnippet("zsh")
	if !strings.Contains(z, "rcd() {") || !strings.Contains(z, "command rig cd") || !strings.Contains(z, "builtin cd --") {
		t.Fatalf("zsh rcd should be a self-contained cd function:\n%s", z)
	}
	if strings.Contains(z, "alias rcd=") {
		t.Fatalf("rcd must be a function, not a plain alias:\n%s", z)
	}
	if f := aliasSnippet("fish"); !strings.Contains(f, "function rcd") || !strings.Contains(f, "command rig cd $argv") {
		t.Fatalf("fish rcd missing its function:\n%s", f)
	}
	if p := aliasSnippet("powershell"); !strings.Contains(p, "function rcd {") || !strings.Contains(p, "Set-Location -LiteralPath") {
		t.Fatalf("powershell rcd missing its function:\n%s", p)
	}
}

func TestAliasSnippet_FishAndPowershellSyntax(t *testing.T) {
	fish := aliasSnippet("fish")
	if !strings.Contains(fish, "alias rr 'rig run'") {
		t.Fatalf("fish snippet should use fish alias syntax:\n%s", fish)
	}
	if strings.Contains(fish, "alias rr=") {
		t.Fatalf("fish snippet must not use the posix name=value form:\n%s", fish)
	}
	ps := aliasSnippet("powershell")
	// Set-Alias can't carry args, so PowerShell aliases are forwarding funcs.
	if !strings.Contains(ps, "function rrm { rig uninstall @args }") {
		t.Fatalf("powershell snippet should define forwarding functions:\n%s", ps)
	}
	// ri is a read-only built-in alias for Remove-Item; the block must clear it
	// first or `ri` would delete instead of installing.
	if !strings.Contains(ps, "Remove-Item Alias:ri -Force -ErrorAction SilentlyContinue; function ri {") {
		t.Fatalf("powershell snippet must clear the built-in ri alias before defining it:\n%s", ps)
	}
}

func TestAliasSet_UninstallIsRrmAndNothingShadowsRun(t *testing.T) {
	// Check the source of truth directly, so adding a bad entry like
	// {name: "run", verb: "uninstall"} actually fails the test.
	sawUninstall := false
	for _, a := range rigAliases {
		if a.name == "run" {
			t.Fatalf("no alias may claim the name %q (it shadows the run command); got verb %q", a.name, a.verb)
		}
		if a.verb == "uninstall" {
			sawUninstall = true
			if a.name != "rrm" {
				t.Fatalf("uninstall alias = %q, want rrm", a.name)
			}
		}
	}
	if !sawUninstall {
		t.Fatal("expected an alias for the uninstall verb")
	}
}

func TestRemoveBlock_StripsBlockAndItsSeparator(t *testing.T) {
	base := "# my rc\nalias ll='ls -l'\n"
	withBlock, changed := spliceBlock(base, aliasSnippet("zsh"), aliasMarkerBegin(), aliasMarkerEnd())
	if !changed {
		t.Fatal("splice should report a change")
	}
	got, changed := removeBlock(withBlock, aliasMarkerBegin(), aliasMarkerEnd())
	if !changed {
		t.Fatal("remove should report a change")
	}
	if got != base {
		t.Fatalf("remove must restore the original rc exactly:\n%q\nwant:\n%q", got, base)
	}
	// Removing again is a no-op.
	if _, changed := removeBlock(got, aliasMarkerBegin(), aliasMarkerEnd()); changed {
		t.Fatal("removing an absent block must report no change")
	}
}

func TestAliasCommand_InstallIsIdempotentAndCoexistsWithSetup(t *testing.T) {
	home := fakeHome(t)
	rc := filepath.Join(home, ".zshrc")

	// A pre-existing setup block must survive alias install/remove untouched.
	if _, err := installSnippet(rc, setupSnippet("zsh", "rig"), "rig"); err != nil {
		t.Fatalf("seed setup block: %v", err)
	}

	run := func(args ...string) string {
		t.Helper()
		cmd := newAliasCmd()
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("alias %v: %v", args, err)
		}
		return buf.String()
	}

	run("install", "zsh")
	data, _ := os.ReadFile(rc)
	if !strings.Contains(string(data), "alias rr='rig run'") {
		t.Fatalf("rc missing the alias block:\n%s", data)
	}
	if !strings.Contains(string(data), markerBegin("rig")) {
		t.Fatal("alias install must not disturb the existing setup block")
	}

	if out := run("install", "zsh"); !strings.Contains(out, "already installed") {
		t.Fatalf("re-install output = %q, want the already-installed notice", out)
	}
	after, _ := os.ReadFile(rc)
	if string(after) != string(data) {
		t.Fatal("a re-run must leave the rc file byte-identical")
	}

	run("remove", "zsh")
	final, _ := os.ReadFile(rc)
	if strings.Contains(string(final), aliasMarkerBegin()) {
		t.Fatalf("remove must strip the alias block:\n%s", final)
	}
	if !strings.Contains(string(final), markerBegin("rig")) {
		t.Fatal("remove must leave the setup block intact")
	}
}

func TestAliasCommand_OnlyInstallsSelectedAliases(t *testing.T) {
	home := fakeHome(t)
	rc := filepath.Join(home, ".zshrc")

	cmd := newAliasCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"install", "zsh", "--only", "rb,rt"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("alias install --only: %v", err)
	}
	data, _ := os.ReadFile(rc)
	if !strings.Contains(string(data), "alias rb='rig build'") || !strings.Contains(string(data), "alias rt='rig test'") {
		t.Fatalf("rc missing the selected aliases:\n%s", data)
	}
	if strings.Contains(string(data), "alias rr='rig run'") || strings.Contains(string(data), "rcd() {") {
		t.Fatalf("rc should carry only the selected aliases:\n%s", data)
	}
}

func TestAliasCommand_OnlyRejectsUnknownName(t *testing.T) {
	fakeHome(t)
	cmd := newAliasCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"install", "zsh", "--only", "rb,nope"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "unknown alias") || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("err = %v, want an unknown-alias error naming nope", err)
	}
}

func TestAliasCommand_AllAndOnlyAreMutuallyExclusive(t *testing.T) {
	fakeHome(t)
	cmd := newAliasCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"install", "zsh", "--all", "--only", "rb"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("--all and --only together should be rejected")
	}
}

// resolveAliasSelection falls back to the full set off a TTY (as in tests, CI,
// and `rig setup --aliases`), so a plain non-interactive install is unchanged.
func TestResolveAliasSelection_NonInteractiveTakesFullSet(t *testing.T) {
	sel, cancelled, clearAll, err := resolveAliasSelection(nil, false, false, rigAliases)
	if err != nil || cancelled || clearAll {
		t.Fatalf("resolveAliasSelection = (_, %v, %v, %v), want the full set", cancelled, clearAll, err)
	}
	if len(sel) != len(rigAliases) {
		t.Fatalf("got %d aliases, want the full %d", len(sel), len(rigAliases))
	}
}

// installedAliases powers the checklist's pre-check state on re-run: it must
// report exactly the aliases a prior block wrote, including the rcd function.
func TestInstalledAliases_ReportsWhatTheBlockDefines(t *testing.T) {
	for _, shell := range setupShells {
		block := aliasSnippetFor(shell, []rigAlias{
			{"rb", "build", "", false},
			{"rcd", "cd", "", true},
		})
		got := installedAliases(shell, block)
		if len(got) != 2 || got[0].name != "rb" || got[1].name != "rcd" {
			t.Fatalf("%s: installedAliases = %v, want [rb rcd]", shell, aliasNamesOf(got))
		}
		// A candidate the block doesn't define must not be reported.
		if inst := installedAliases(shell, block); aliasNamesOf(inst) == aliasNames() {
			t.Fatalf("%s: should not report the full set for a 2-alias block", shell)
		}
	}
}

func TestExtractBlock_RoundTripsWithSplice(t *testing.T) {
	snippet := aliasSnippetFor("zsh", rigAliases)
	rc, _ := spliceBlock("# rc\n", snippet, aliasMarkerBegin(), aliasMarkerEnd())
	block, ok := extractBlock(rc, aliasMarkerBegin(), aliasMarkerEnd())
	if !ok || block != snippet {
		t.Fatalf("extractBlock = (%q, %v), want the spliced snippet", block, ok)
	}
	if _, ok := extractBlock("# no block here\n", aliasMarkerBegin(), aliasMarkerEnd()); ok {
		t.Fatal("extractBlock must report ok=false when there's no block")
	}
}

func TestAliasCommand_PrintWritesNothing(t *testing.T) {
	home := fakeHome(t)
	cmd := newAliasCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"install", "fish", "--print"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("alias install --print: %v", err)
	}
	if !strings.Contains(buf.String(), "alias rr 'rig run'") {
		t.Fatalf("output = %q, want the fish snippet", buf.String())
	}
	// The install target is fish, so --print must not have written fish's own
	// startup file (nor any other).
	if _, err := os.Stat(filepath.Join(home, ".config", "fish", "config.fish")); !os.IsNotExist(err) {
		t.Fatal("--print must not write fish's config.fish")
	}
	if _, err := os.Stat(filepath.Join(home, ".zshrc")); !os.IsNotExist(err) {
		t.Fatal("--print must not write any rc file")
	}
}

func TestSetupCommand_AliasesFlagInstallsBothBlocks(t *testing.T) {
	home := fakeHome(t)
	rc := filepath.Join(home, ".zshrc")

	cmd := newSetupCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"zsh", "--aliases"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("setup --aliases: %v", err)
	}
	data, _ := os.ReadFile(rc)
	if !strings.Contains(string(data), markerBegin("rig")) {
		t.Fatalf("rc missing the setup block:\n%s", data)
	}
	if !strings.Contains(string(data), aliasMarkerBegin()) || !strings.Contains(string(data), "alias rr='rig run'") {
		t.Fatalf("rc missing the alias block:\n%s", data)
	}

	// `rig alias remove` still cleanly strips only the alias block.
	rm := newAliasCmd()
	rm.SetOut(&bytes.Buffer{})
	rm.SetErr(&bytes.Buffer{})
	rm.SetArgs([]string{"remove", "zsh"})
	if err := rm.Execute(); err != nil {
		t.Fatalf("alias remove: %v", err)
	}
	final, _ := os.ReadFile(rc)
	if strings.Contains(string(final), aliasMarkerBegin()) {
		t.Fatalf("alias block should be gone:\n%s", final)
	}
	if !strings.Contains(string(final), markerBegin("rig")) {
		t.Fatal("setup block must survive alias remove")
	}
}

func TestSetupCommand_PlainRunSuggestsAliases(t *testing.T) {
	fakeHome(t)
	cmd := newSetupCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"zsh"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if !strings.Contains(buf.String(), "--aliases") {
		t.Fatalf("plain setup should hint at --aliases:\n%s", buf.String())
	}
}

func TestAliasCommand_RejectsAnUnknownShell(t *testing.T) {
	fakeHome(t)
	t.Setenv("SHELL", "/bin/tcsh")
	cmd := newAliasCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"install"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), `unknown shell "tcsh"`) {
		t.Fatalf("err = %v, want the unknown-shell message", err)
	}
}
