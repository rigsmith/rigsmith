package fang_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
	"github.com/rigsmith/core/fang"
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
