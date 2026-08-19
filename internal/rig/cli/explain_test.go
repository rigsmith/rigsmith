package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/internal/rig/config"
	"github.com/spf13/cobra"
)

// explainRepo builds a Node repo with the given .rig.json and makes it the
// working directory, returning its root.
func explainRepo(t *testing.T, rigJSON string) string {
	t.Helper()
	isolateGlobalConfig(t)
	root := t.TempDir()
	writeRigJSON(t, root, rigJSON)
	if err := os.WriteFile(filepath.Join(root, "package.json"),
		[]byte(`{"name":"app","scripts":{"build":"webpack","storybook":"start-storybook"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	return root
}

// runExplain runs `rig explain args…` against the built-in tree and returns its
// stdout.
func runExplain(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := newExplainCmd()
	cmd.SetContext(context.Background())
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

// The motivating case: a custom shell command, explained without running it —
// the resolved line, where it runs, and where it came from.
func TestExplainCustomCommand(t *testing.T) {
	root := explainRepo(t, `{
	  "ecosystem": "node",
	  "commands": { "markers": "grep -rho 'sheepish-[a-z-]*' src | sort -u" }
	}`)

	out, err := runExplain(t, "markers")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"grep -rho 'sheepish-[a-z-]*' src | sort -u", // the command, as it will run
		"custom command",                     // where the verb came from
		filepath.Join(root, config.FileName), // …and which file declares it
		root,                                 // the working directory
		"portable",                           // which shell interprets it
	} {
		if !strings.Contains(out, want) {
			t.Errorf("explain output missing %q; got:\n%s", want, out)
		}
	}
}

// The one property that matters: what explain prints is what a run runs. Both
// go through customPlan, and this asserts they haven't drifted apart — an
// explain that can disagree with a run is worse than no explain at all.
func TestExplainAgreesWithTheRun(t *testing.T) {
	explainRepo(t, `{
	  "ecosystem": "node",
	  "commands": { "markers": "grep -rho 'sheepish-[a-z0-9-]*' src | sort -u" }
	}`)

	explained, err := runExplain(t, "markers")
	if err != nil {
		t.Fatal(err)
	}

	// The same verb, echoed by the real run path under --dry-run.
	dryRun = true
	t.Cleanup(func() { dryRun = false })
	host, echoed := newRunHost()
	cwd, _ := os.Getwd()
	cfg, _ := config.LoadMerged(resolveRoot(cwd))
	if err := runCustom(host, cfg, resolveRoot(cwd), "markers", cfg.Commands["markers"], nil); err != nil {
		t.Fatal(err)
	}

	line := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(echoed.String()), "→"))
	if line == "" {
		t.Fatal("the run echoed nothing to compare against")
	}
	if !strings.Contains(explained, line) {
		t.Errorf("explain does not describe what runs.\n  run:     %q\n  explain:\n%s", line, explained)
	}
}

// Args are folded in the way the run folds them, so an explained invocation is
// the invocation.
func TestExplainFoldsPassthroughArgs(t *testing.T) {
	explainRepo(t, `{"ecosystem":"node","commands":{"markers":"grep -rho x src"}}`)

	out, err := runExplain(t, "markers", "--color", "two words")
	if err != nil {
		t.Fatal(err)
	}
	if want := `grep -rho x src --color 'two words'`; !strings.Contains(out, want) {
		t.Errorf("want the folded command %q; got:\n%s", want, out)
	}
}

// The argv form is exec'd directly; explain says so, because "no shell" is why
// a pipe or a glob in it would not do what it looks like it does.
func TestExplainArgvCommandSaysNoShell(t *testing.T) {
	explainRepo(t, `{"ecosystem":"node","commands":{"gen":["node","tools/gen.js","--all"]}}`)

	out, err := runExplain(t, "gen")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "node tools/gen.js --all") || !strings.Contains(out, "no shell") {
		t.Errorf("want the argv and a no-shell note; got:\n%s", out)
	}
}

// Every layer rig contributes is listed with the layer that set it, and a value
// the ambient environment overrides is marked — the stated value would
// otherwise be one the command never sees.
func TestExplainListsTheEnvironmentWithItsProvenance(t *testing.T) {
	root := explainRepo(t, `{
	  "ecosystem": "node",
	  "env": { "FROM_CONFIG": "config" },
	  "commands": { "sh": { "command": "env", "env": { "FROM_COMMAND": "command" } } }
	}`)
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("FROM_FILE=file\nSHADOWED=file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHADOWED", "ambient")

	out, err := runExplain(t, "sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"FROM_FILE=file",
		"FROM_CONFIG=config",
		".rig.json env",
		"FROM_COMMAND=command",
		"command env",
		"SHADOWED=ambient",
		"overridden by the ambient environment",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("environment listing missing %q; got:\n%s", want, out)
		}
	}
}

// A built-in verb is explained through the same resolver the dev verbs use, and
// says which ecosystem (and package manager) decided it.
func TestExplainEcosystemVerb(t *testing.T) {
	explainRepo(t, `{"ecosystem":"node"}`)

	out, err := runExplain(t, "build")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "ecosystem convention") || !strings.Contains(out, "run build") {
		t.Errorf("want the ecosystem's build command; got:\n%s", out)
	}
}

// A verb that assembles its command as it runs is not guessed at. Printing a
// plausible command nobody executes is the failure explain exists to prevent.
func TestExplainRefusesVerbsItCannotResolveUpFront(t *testing.T) {
	explainRepo(t, `{"ecosystem":"node"}`)

	_, err := runExplain(t, "coverage")
	if err == nil {
		t.Fatal("want an error rather than a guess for coverage")
	}
	if !strings.Contains(err.Error(), "--dry-run") {
		t.Errorf("the error should point at --dry-run; got: %v", err)
	}
}

// An argument to a built-in verb selects a project or a filter at run time, so
// explain declines to model it and names the command that does.
func TestExplainRefusesArgsToABuiltinVerb(t *testing.T) {
	explainRepo(t, `{"ecosystem":"node"}`)

	_, err := runExplain(t, "test", "MyClass")
	if err == nil {
		t.Fatal("want an error for an argument to a built-in verb")
	}
	// --dry-run leads the arguments: appended after them it would land past a
	// `--` and be forwarded to the ecosystem command instead of enabling
	// anything.
	if !strings.Contains(err.Error(), "rig test --dry-run MyClass") {
		t.Errorf("the error should quote the --dry-run form back; got: %v", err)
	}
}

// A custom command named after a built-in verb never runs. Explaining that verb
// is exactly when someone is asking why, so the collision is reported there.
func TestExplainReportsAShadowedCustomCommand(t *testing.T) {
	explainRepo(t, `{"ecosystem":"node","commands":{"build":"echo mine"}}`)

	out, err := runExplain(t, "build")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "built-in rig verb") || !strings.Contains(out, "never runs") {
		t.Errorf("want the shadowing called out; got:\n%s", out)
	}
	if strings.Contains(out, "echo mine") {
		t.Errorf("the shadowed entry must not be presented as what runs; got:\n%s", out)
	}
}

// The same collision is reported on load, for every command — the symptom
// (rig ignoring your config) shows up long before anyone thinks to run explain.
func TestShadowedCommandWarnsOnLoad(t *testing.T) {
	explainRepo(t, `{"ecosystem":"node","commands":{"build":"echo mine","fine":"echo ok"}}`)
	cwd, _ := os.Getwd()
	cfg, _ := config.LoadMerged(resolveRoot(cwd))

	if got := shadowedCommands(cfg); len(got) != 1 || got[0] != "build" {
		t.Fatalf("shadowedCommands = %v, want just [build]", got)
	}

	cmd := &cobra.Command{Use: "build"}
	var errOut bytes.Buffer
	cmd.SetErr(&errOut)
	reportConfigProblems(cmd, cfg)
	if !strings.Contains(errOut.String(), `"build"`) {
		t.Errorf("want a warning naming the shadowed command; got: %q", errOut.String())
	}
}

// Config problems collected at parse time were never surfaced anywhere; they
// are now reported on the same channel.
func TestParseWarningsAreReported(t *testing.T) {
	cmd := &cobra.Command{Use: "build"}
	var errOut bytes.Buffer
	cmd.SetErr(&errOut)

	reportConfigProblems(cmd, config.Config{Warnings: []string{"unknown key \"commmands\""}})

	if !strings.Contains(errOut.String(), "commmands") {
		t.Errorf("want the parse warning printed; got: %q", errOut.String())
	}
}

// `rig info` is the report about the config, so the problems with it belong in
// that report — both kinds, in one section.
func TestInfoShowsConfigWarnings(t *testing.T) {
	explainRepo(t, `{"ecosystem":"node","commmands":{},"commands":{"build":"echo mine"}}`)

	out, err := runSub(t, newInfoCmd())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Warnings",
		`unknown key "commmands"`, // a parse warning, collected but never shown before
		"built-in rig verb",       // …and the shadowed command
	} {
		if !strings.Contains(out, want) {
			t.Errorf("info output missing %q; got:\n%s", want, out)
		}
	}
}

// A clean config gets no section — an empty "Warnings" heading reads as a
// finding of its own.
func TestInfoOmitsTheWarningsSectionWhenClean(t *testing.T) {
	explainRepo(t, `{"ecosystem":"node","commands":{"markers":"grep -r x src"}}`)

	out, err := runSub(t, newInfoCmd())
	if err != nil {
		t.Fatal(err)
	}
	// Match the heading as its own line: the temp root printed above carries
	// the test's name, which contains the word.
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "Warnings" {
			t.Errorf("clean config should print no Warnings section; got:\n%s", out)
		}
	}
}

// info and explain print the problems themselves, so the per-run notice stays
// out of their way rather than saying everything twice.
func TestCommandsThatReportProblemsThemselvesAreNotAlsoWarnedAt(t *testing.T) {
	cfg := config.Config{Warnings: []string{"unknown key"}}
	for _, name := range []string{"info", "explain"} {
		cmd := &cobra.Command{Use: name}
		var errOut bytes.Buffer
		cmd.SetErr(&errOut)

		reportConfigProblems(cmd, cfg)

		if errOut.Len() != 0 {
			t.Errorf("%s: want no duplicate notice, got %q", name, errOut.String())
		}
	}
}

// Completion output is parsed by a shell, so nothing is written alongside it.
func TestCompletionRequestsAreNotWarnedAt(t *testing.T) {
	cmd := &cobra.Command{Use: cobra.ShellCompRequestCmd}
	var errOut bytes.Buffer
	cmd.SetErr(&errOut)

	reportConfigProblems(cmd, config.Config{Warnings: []string{"unknown key"}})

	if errOut.Len() != 0 {
		t.Errorf("completion must not be polluted with warnings; got: %q", errOut.String())
	}
}

// Bare `rig explain` is the overview: the ecosystem's verbs, then everything
// the repo adds, each with the command it becomes.
func TestExplainAllListsEveryVerbTheRepoResolves(t *testing.T) {
	explainRepo(t, `{"ecosystem":"node","commands":{"markers":"grep -r x src"}}`)

	out, err := runExplain(t)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Ecosystem verbs", "build", "run build",
		"Custom commands", "markers", "grep -r x src",
		"package.json scripts", "storybook",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("overview missing %q; got:\n%s", want, out)
		}
	}
}

// An alias explains the verb it runs, resolved through the same command tree
// that dispatches it rather than a second table of aliases.
func TestExplainResolvesAliases(t *testing.T) {
	explainRepo(t, `{"ecosystem":"node"}`)

	cmd := newRootCmd()
	if got := canonicalVerb(cmd, "fmt"); got != "format" {
		t.Errorf("canonicalVerb(fmt) = %q, want format", got)
	}
	if got := canonicalVerb(cmd, "nonesuch"); got != "nonesuch" {
		t.Errorf("an unknown name should pass through, got %q", got)
	}
}

// A misconfigured command reports the same error explain would hit at run time,
// rather than a blank line in the listing.
func TestExplainSurfacesAResolutionError(t *testing.T) {
	explainRepo(t, `{"ecosystem":"node","commands":{"broken":{"os":{"plan9":"echo hi"}}}}`)

	_, err := runExplain(t, "broken")
	if err == nil || !strings.Contains(err.Error(), "no command defined for this OS") {
		t.Fatalf("want the run's own resolution error, got %v", err)
	}
}

// --dry-run must sit BEFORE any `--`, or it is forwarded to the ecosystem
// command and enables nothing — and the separator itself has to be reinserted,
// since cobra strips it out of args.
func TestDryRunSuggestionPutsTheFlagBeforeTheSeparator(t *testing.T) {
	root := &cobra.Command{Use: "rig"}
	c := &cobra.Command{Use: "explain", RunE: func(*cobra.Command, []string) error { return nil }}
	c.Flags().SetInterspersed(false)
	root.AddCommand(c)
	root.SetArgs([]string{"explain", "build", "--", "--target=x"})
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}

	// explain sets SetInterspersed(false), so cobra hands the separator through
	// IN args and reports ArgsLenAtDash as -1 — verified against cobra rather
	// than assumed. The suggestion therefore passes args through verbatim, and
	// only has to put --dry-run in front of them.
	got := dryRunSuggestion(c, "build", []string{"--", "--target=x"})
	want := "rig build --dry-run -- --target=x"
	if got != want {
		t.Fatalf("dryRunSuggestion = %q, want %q", got, want)
	}
}

// The other parsing mode, in case explain ever stops suppressing interspersal:
// cobra then strips the separator and records its position, and the suggestion
// has to put it back — otherwise the forwarded flags would be parsed by rig.
func TestDryRunSuggestionReinsertsAStrippedSeparator(t *testing.T) {
	root := &cobra.Command{Use: "rig"}
	c := &cobra.Command{Use: "explain", RunE: func(*cobra.Command, []string) error { return nil }}
	root.AddCommand(c) // interspersed left on
	root.SetArgs([]string{"explain", "build", "--", "--target=x"})
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if got, want := dryRunSuggestion(c, "build", []string{"--target=x"}), "rig build --dry-run -- --target=x"; got != want {
		t.Fatalf("dryRunSuggestion = %q, want %q", got, want)
	}
}

// Without a separator the arguments follow the flag unchanged.
func TestDryRunSuggestionWithoutASeparator(t *testing.T) {
	root := &cobra.Command{Use: "rig"}
	c := &cobra.Command{Use: "explain", RunE: func(*cobra.Command, []string) error { return nil }}
	root.AddCommand(c)
	root.SetArgs([]string{"explain", "test", "MyClass"})
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}

	if got, want := dryRunSuggestion(c, "test", []string{"MyClass"}), "rig test --dry-run MyClass"; got != want {
		t.Fatalf("dryRunSuggestion = %q, want %q", got, want)
	}
}

// The root command is only what runs when the workspace dispatch lets it, so
// explain and the run path must agree about when that is.
func TestRootCommandStandsMatchesTheDispatch(t *testing.T) {
	cases := []struct {
		name           string
		verb           string
		rootHasPackage bool
		tasks, scripts int
		want           bool
	}{
		{"runnable root", "run", true, 3, 2, true},
		{"nothing to offer", "build", false, 0, 0, true},
		{"go mains under cmd/", "run", false, 3, 0, false},
		{"run with only scripts", "run", false, 0, 2, false},
		{"lone subpackage, --all verb", "build", false, 1, 0, true},
		{"several packages, --all verb", "build", false, 2, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := rootCommandStands(tc.verb, tc.rootHasPackage, tc.tasks, tc.scripts); got != tc.want {
				t.Fatalf("rootCommandStands = %v, want %v", got, tc.want)
			}
		})
	}
}
