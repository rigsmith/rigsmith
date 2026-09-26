package fang_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/rigsmith/rigsmith/core/fang"
	"github.com/spf13/cobra"
)

// runHelp executes root with the given args and returns its (plain, no-ANSI)
// output. A buffer sink makes colorprofile fall back to ASCII, so assertions
// can match on raw text.
func runHelp(t *testing.T, root *cobra.Command, opts []fang.Option, args ...string) string {
	t.Helper()
	t.Setenv("__FANG_TEST_WIDTH", "120")
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(args)
	if err := fang.Execute(context.Background(), root, opts...); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	return buf.String()
}

// Local fork behavior: a command's aliases are listed in the help command list,
// in their own column (comma-joined) between the command and its description.
func TestHelpListsAliases(t *testing.T) {
	root := &cobra.Command{Use: "demo", Run: func(*cobra.Command, []string) {}}
	root.AddCommand(&cobra.Command{
		Use:     "format",
		Short:   "Format the code",
		Aliases: []string{"fmt", "f"},
		Run:     func(*cobra.Command, []string) {},
	})

	out := runHelp(t, root, nil, "--help")
	// The command, its aliases column, and the description share one line.
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "format") {
			fmtIdx := strings.Index(line, "format")
			aliasIdx := strings.Index(line, "fmt, f")
			descIdx := strings.Index(line, "Format the code")
			if aliasIdx < 0 || !(fmtIdx < aliasIdx && aliasIdx < descIdx) {
				t.Errorf("expected command | aliases | description order; got line:\n%q", line)
			}
			return
		}
	}
	t.Errorf("command 'format' not found in help; got:\n%s", out)
}

// Local fork behavior (upstream #88): WithHelpAppender content is rendered
// after the standard help body.
func TestWithHelpAppender(t *testing.T) {
	root := &cobra.Command{Use: "demo", Short: "Demo", Run: func(*cobra.Command, []string) {}}
	opts := []fang.Option{
		fang.WithHelpAppender(func(w *colorprofile.Writer, _ *cobra.Command, _ fang.Styles) {
			_, _ = w.WriteString("APPENDED SECTION\n")
		}),
	}

	out := runHelp(t, root, opts, "--help")
	if !strings.Contains(out, "APPENDED SECTION") {
		t.Errorf("appender output missing; got:\n%s", out)
	}
}

// Local fork behavior (rigsmith): WithBanner heads the root command's help and
// stands in for the default `--version` line, but never appears on subcommand
// help (it's a root-level identity, not a per-screen header).
func TestWithBanner(t *testing.T) {
	const marker = "RIGSMITH-BANNER"
	newRoot := func() *cobra.Command {
		root := &cobra.Command{Use: "demo", Short: "Demo", Run: func(*cobra.Command, []string) {}}
		root.AddCommand(&cobra.Command{Use: "sub", Short: "A subcommand", Run: func(*cobra.Command, []string) {}})
		return root
	}
	opts := []fang.Option{
		fang.WithVersion("1.2.3"),
		fang.WithBanner(func(version string) string { return marker + " " + version }),
	}

	// Root help: banner first, then the usual help body.
	rootHelp := runHelp(t, newRoot(), opts, "--help")
	if !strings.HasPrefix(strings.TrimLeft(rootHelp, "\n"), marker) {
		t.Errorf("banner should head the root help; got:\n%s", rootHelp)
	}

	// Subcommand help: no banner.
	subHelp := runHelp(t, newRoot(), opts, "sub", "--help")
	if strings.Contains(subHelp, marker) {
		t.Errorf("banner should not appear on subcommand help; got:\n%s", subHelp)
	}

	// Version, piped (the buffer isn't a terminal): the bare number, for a
	// script to read. The banner case is in version_test.go.
	ver := runHelp(t, newRoot(), opts, "--version")
	if ver != "1.2.3\n" {
		t.Errorf("piped --version = %q, want the bare version", ver)
	}
}

// Regression guard for the evalGroups signature change carried for upstream #97
// (which makes evalGroups defensive about groups that aren't registered — a
// state cobra itself rejects at Execute, so we only assert the normal path):
// a registered group renders its title and its commands.
func TestRegisteredGroupRendered(t *testing.T) {
	root := &cobra.Command{Use: "demo", Run: func(*cobra.Command, []string) {}}
	root.AddGroup(&cobra.Group{ID: "io", Title: "input/output"})
	root.AddCommand(&cobra.Command{
		Use:     "load",
		Short:   "Load a file",
		GroupID: "io",
		Run:     func(*cobra.Command, []string) {},
	})

	// The Title style upper-cases group headers, so match case-insensitively.
	out := runHelp(t, root, nil, "--help")
	if !strings.Contains(strings.ToLower(out), "input/output") || !strings.Contains(out, "load") {
		t.Errorf("registered group title/command missing; got:\n%s", out)
	}
}

// Local fork behavior (rigsmith): an error whose message runs past one line
// carries a layout — a headline, then an explanation and the command that fixes
// it. Only the headline is treated as the error sentence (wrapped to the
// terminal, ended with a period); the rest keeps the lines and indentation it was written
// with, so a command line stays copy-pasteable on a narrow terminal.
func TestMultiLineErrorKeepsItsLayout(t *testing.T) {
	// NOT t.Setenv("__FANG_TEST_WIDTH", …): width is a package-level
	// sync.OnceValue that earlier tests in this file have already resolved to
	// 120, so setting it here would have no effect and the assertion below could
	// pass against the old wrapping behaviour purely because the line is short.
	// Use a fix line longer than the maximum cached width instead, so wrapping
	// would definitely mangle it if the layout were not preserved.
	const fix = "rig build -- --target=brave_browser_tests,brave_unit_tests,brave_installer_tests --config=Release --jobs=16 --out-dir=out/Release_x64"
	root := &cobra.Command{
		Use:          "demo",
		SilenceUsage: true,
		RunE: func(*cobra.Command, []string) error {
			return errors.New("unknown flag: --target\n\nan explanation long enough that the sentence above it would have been wrapped\n\n    " + fix)
		},
	}

	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(nil)
	if err := fang.Execute(context.Background(), root); err == nil {
		t.Fatal("want the command's error back")
	}
	out := buf.String()

	// The headline is rendered as the error sentence.
	if !strings.Contains(out, "unknown flag: --target.") {
		t.Errorf("headline missing or unstyled; got:\n%s", out)
	}
	// The fix survives on one line, indented, despite the 40-column width.
	if !strings.Contains(out, "    "+fix) {
		t.Errorf("the fix was reflowed or lost; got:\n%s", out)
	}
}

// A single-line error is unchanged by the multi-line handling: one sentence,
// wrapped as before.
func TestSingleLineErrorIsUnchanged(t *testing.T) {
	t.Setenv("__FANG_TEST_WIDTH", "120")
	root := &cobra.Command{
		Use:          "demo",
		SilenceUsage: true,
		RunE:         func(*cobra.Command, []string) error { return errors.New("no test project found") },
	}

	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(nil)
	if err := fang.Execute(context.Background(), root); err == nil {
		t.Fatal("want the command's error back")
	}
	if out := buf.String(); !strings.Contains(out, "no test project found.") {
		t.Errorf("single-line error changed; got:\n%s", out)
	}
}

// Local fork behavior (rigsmith): the error sentence is shown as written. The
// first word is never title-cased (a message that opens with a flag or a
// package name would read "--Only" or "App"), and the closing period is left
// off when the headline already ends in punctuation of its own.
func TestErrorSentenceIsShownAsWritten(t *testing.T) {
	cases := []struct {
		msg       string
		want      string
		forbidden []string
	}{
		{
			msg:       "--only names nosuchpkg, which is not in the workspace; is it misspelled?",
			want:      "--only names nosuchpkg, which is not in the workspace; is it misspelled?",
			forbidden: []string{"--Only", "misspelled?."},
		},
		{
			msg:       "--changelog github needs a GitHub remote to link to; this repository has none",
			want:      "--changelog github needs a GitHub remote to link to; this repository has none.",
			forbidden: []string{"--Changelog"},
		},
		{
			msg:       "app depends on the skipped package core, but app is not being skipped: add it to `ignore` in the config too",
			want:      "app depends on the skipped package core",
			forbidden: []string{"App depends"},
		},
		{
			msg:       "couldn't tell what is already published:\nthe registry said no",
			want:      "couldn't tell what is already published:",
			forbidden: []string{"published:.", "Couldn't"},
		},
		{
			msg:       "set `dotnet.auth`",
			want:      "set `dotnet.auth`",
			forbidden: []string{"`dotnet.auth`."},
		},
		{
			msg:       `no package named "core"`,
			want:      `no package named "core"`,
			forbidden: []string{`"core".`},
		},
	}
	for _, tc := range cases {
		root := &cobra.Command{
			Use:          "demo",
			SilenceUsage: true,
			RunE:         func(*cobra.Command, []string) error { return errors.New(tc.msg) },
		}
		var buf bytes.Buffer
		root.SetOut(&buf)
		root.SetErr(&buf)
		root.SetArgs(nil)
		if err := fang.Execute(context.Background(), root); err == nil {
			t.Fatal("want the command's error back")
		}
		out := buf.String()
		if !strings.Contains(out, tc.want) {
			t.Errorf("%q: want %q in the output; got:\n%s", tc.msg, tc.want, out)
		}
		for _, bad := range tc.forbidden {
			if strings.Contains(out, bad) {
				t.Errorf("%q: output has %q; got:\n%s", tc.msg, bad, out)
			}
		}
	}
}

// A caller's ErrorText transform styles the headline only: the detail lines
// and the "for usage." tail are shown as written, so a command meant to be
// copied (--target=Release) comes out the way it has to be typed.
func TestErrorTextTransformLeavesTheDetailAlone(t *testing.T) {
	styles := fang.Styles{
		ErrorText:   lipgloss.NewStyle().Transform(strings.ToUpper),
		ErrorHeader: lipgloss.NewStyle().SetString("ERROR"),
	}
	var buf bytes.Buffer
	fang.DefaultErrorHandler(&buf, styles, errors.New("unknown flag: --target\n\n    rig build -- --target=Release"))
	out := buf.String()
	if !strings.Contains(out, "UNKNOWN FLAG: --TARGET.") {
		t.Errorf("the headline didn't take the caller's transform; got:\n%s", out)
	}
	if !strings.Contains(out, "    rig build -- --target=Release") || strings.Contains(out, "--TARGET=RELEASE") {
		t.Errorf("the transform reached the detail line; got:\n%s", out)
	}
	if !strings.Contains(out, "for usage.") {
		t.Errorf("the transform reached the usage tail; got:\n%s", out)
	}
}
