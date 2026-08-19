package cli

import (
	"io"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// parseArgs runs the built-in tree over args far enough to parse flags, with the
// same args standing in for the process command line the hint quotes back. No
// verb body runs: every case here fails during flag parsing.
func parseArgs(t *testing.T, args ...string) error {
	t.Helper()
	prev := processArgs
	processArgs = func() []string { return args }
	t.Cleanup(func() { processArgs = prev })

	root := NewRootCmd()
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs(args)
	err := root.Execute()
	if err == nil {
		t.Fatalf("rig %s: want a flag error, got none", strings.Join(args, " "))
	}
	return err
}

// The headline names the flag rig rejected, and the fix is the user's own
// command line with `--` inserted — copy-pasteable, not a generic example.
func TestUnknownFlagNamesTheFlagAndQuotesTheFixBack(t *testing.T) {
	err := parseArgs(t, "build", "--target=brave_browser_tests")

	msg := err.Error()
	headline, _, _ := strings.Cut(msg, "\n")
	if headline != "unknown flag: --target" {
		t.Errorf("headline = %q, want it to name --target", headline)
	}
	for _, want := range []string{
		"rig build doesn't take --target",
		"put it after --:",
		"rig build -- --target=brave_browser_tests",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q; got:\n%s", want, msg)
		}
	}
}

// The `--` goes in at the first flag rig didn't recognize, so the tokens the
// verb did understand keep their place and one edit fixes a line with several
// unknown flags on it.
func TestUnknownFlagInsertsTheSeparatorAtTheOffendingFlag(t *testing.T) {
	err := parseArgs(t, "test", "--all", "--reporter", "dot", "--bail")

	if want := "rig test --all -- --reporter dot --bail"; !strings.Contains(err.Error(), want) {
		t.Errorf("want the fix %q; got:\n%s", want, err.Error())
	}
}

// Every verb that forwards gets the same treatment — the dead end was never
// specific to build.
func TestUnknownFlagHintCoversEveryForwardingVerb(t *testing.T) {
	for _, verb := range []string{"build", "test", "run", "format", "lint", "typecheck", "clean", "rebuild", "install", "ci", "add", "global", "dlx"} {
		msg := parseArgs(t, verb, "--nonesuch").Error()
		if want := "rig " + verb + " -- --nonesuch"; !strings.Contains(msg, want) {
			t.Errorf("rig %s: want the fix %q; got:\n%s", verb, want, msg)
		}
	}
}

// An argument that needs quoting survives the round trip, so the suggested line
// can be pasted as-is.
func TestUnknownFlagQuotesArgumentsInTheFix(t *testing.T) {
	err := parseArgs(t, "test", "--grep=two words")

	if want := `rig test -- '--grep=two words'`; !strings.Contains(err.Error(), want) {
		t.Errorf("want the fix %q; got:\n%s", want, err.Error())
	}
}

// A shorthand cluster is reported by the letter that failed, and only that
// letter is forwarded: -a is rig's own --all and keeps its meaning.
func TestUnknownShorthandFlagNamesTheLetterAndSplitsTheCluster(t *testing.T) {
	err := parseArgs(t, "build", "-aZ")

	msg := err.Error()
	if headline, _, _ := strings.Cut(msg, "\n"); headline != "unknown flag: -Z" {
		t.Errorf("headline = %q, want it to name -Z", headline)
	}
	if want := "rig build -a -- -Z"; !strings.Contains(msg, want) {
		t.Errorf("want the fix %q; got:\n%s", want, msg)
	}
}

// A lone unknown shorthand moves across the separator whole.
func TestUnknownShorthandFlagOnItsOwn(t *testing.T) {
	err := parseArgs(t, "test", "-Z")

	if want := "rig test -- -Z"; !strings.Contains(err.Error(), want) {
		t.Errorf("want the fix %q; got:\n%s", want, err.Error())
	}
}

// A near-miss of a flag rig does have is called out as a typo — the reason
// unknown flags are still rejected rather than forwarded — while the
// passthrough form stays available for the case where it wasn't a typo.
func TestUnknownFlagSuggestsANearMiss(t *testing.T) {
	msg := parseArgs(t, "build", "--dry-runn").Error()

	if !strings.Contains(msg, "Did you mean --dry-run?") {
		t.Errorf("want a did-you-mean for --dry-run; got:\n%s", msg)
	}
	if !strings.Contains(msg, "rig build -- --dry-runn") {
		t.Errorf("want the passthrough form as well; got:\n%s", msg)
	}
}

// Two edits over a short name is a different flag, not a typo of one. Guessing
// there sends the reader off to check something they never meant.
func TestNearestFlagKeepsQuietOnADistantName(t *testing.T) {
	cmd := NewRootCmd()
	for name, want := range map[string]string{
		"--dry-runn":   "--dry-run", // one edit, long name
		"--interactiv": "",          // build's own flag, but not on the root
		"--nope":       "",          // two edits from --open, and not a typo of it
		"--quie":       "--quiet",   // one edit
		"--ro":         "",          // too short to guess from
	} {
		if got := nearestFlag(cmd, name); got != want {
			t.Errorf("nearestFlag(%q) = %q, want %q", name, got, want)
		}
	}
}

// A verb that assembles its own argv can't honour a `--`, so it must not
// promise one: pflag's error stands (with a typo hint if there is one).
func TestNonForwardingVerbKeepsThePlainFlagError(t *testing.T) {
	err := parseArgs(t, "worktree", "new", "branch", "--nonesuch")

	if msg := err.Error(); msg != "unknown flag: --nonesuch" {
		t.Errorf("message = %q, want pflag's error unchanged", msg)
	}
}

// Flag errors that aren't about an unknown flag are left exactly as pflag
// worded them — rig has nothing to add to a missing value.
func TestOtherFlagErrorsPassThrough(t *testing.T) {
	err := parseArgs(t, "build", "--filter")

	if msg := err.Error(); msg != "flag needs an argument: --filter" {
		t.Errorf("message = %q, want pflag's error unchanged", msg)
	}
}

// Every forwarding verb states the convention in its own help, so `--` is
// findable before you hit the error rather than only after.
func TestForwardingVerbsDocumentTheSeparatorInHelp(t *testing.T) {
	var checked int
	var walk func(*cobra.Command)
	walk = func(cmd *cobra.Command) {
		if forwardsArgs(cmd) {
			checked++
			if !strings.Contains(cmd.Example, " -- ") {
				t.Errorf("%s: forwarding verb without a `--` example in its help", cmd.CommandPath())
			}
		}
		for _, c := range cmd.Commands() {
			walk(c)
		}
	}
	walk(NewRootCmd())
	if checked == 0 {
		t.Fatal("no forwarding verbs found — the annotation is not being applied")
	}
}

// Args after `--` are for the underlying command and are never read as a
// selector, which is what makes the suggested line work on the paths that
// treat a first arg as a project or a test-class query.
func TestArgsBeforeDashStopsAtTheSeparator(t *testing.T) {
	cmd := &cobra.Command{Use: "test", RunE: func(*cobra.Command, []string) error { return nil }}
	cmd.Flags().Bool("all", false, "")

	var got []string
	cmd.RunE = func(c *cobra.Command, args []string) error {
		got = argsBeforeDash(c, args)
		return nil
	}
	cmd.SetArgs([]string{"MyProject", "--", "--logger=trx"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "MyProject" {
		t.Errorf("selectors = %v, want just [MyProject]", got)
	}
}

// A token pflag consumed as ANOTHER flag's value is not the occurrence that
// failed. Inserting `--` before it would change what the command means rather
// than fix it.
func TestPassthroughLineSkipsAValueThatLooksLikeTheFlag(t *testing.T) {
	c := &cobra.Command{Use: "build"}
	c.Flags().String("root", "", "repo root")
	root := &cobra.Command{Use: "rig"}
	root.AddCommand(c)

	orig := processArgs
	processArgs = func() []string { return []string{"build", "--root", "--target", "--target=foo"} }
	t.Cleanup(func() { processArgs = orig })

	got := passthroughLine(c, "--target")
	// The separator must land before the SECOND --target (the real failure), so
	// --root keeps its value.
	want := "rig build --root --target -- --target=foo"
	if got != want {
		t.Fatalf("passthroughLine =\n  %q\nwant\n  %q", got, want)
	}
}

// Nothing after a literal `--` is a candidate: it is already forwarded.
func TestPassthroughLineStopsAtAnExistingSeparator(t *testing.T) {
	c := &cobra.Command{Use: "test"}
	root := &cobra.Command{Use: "rig"}
	root.AddCommand(c)

	orig := processArgs
	processArgs = func() []string { return []string{"test", "--", "--target"} }
	t.Cleanup(func() { processArgs = orig })

	got := passthroughLine(c, "--target")
	if strings.Count(got, "--target") > 1 {
		t.Fatalf("the already-forwarded token was rewritten: %q", got)
	}
}
