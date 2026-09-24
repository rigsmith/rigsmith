package fang

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// With RIGSMITH_DEV_SRC set (as the -dev/-wt launchers do), the source location
// is that worktree path verbatim.
func TestSourceLocationUsesDevSrc(t *testing.T) {
	t.Setenv("RIGSMITH_DEV_SRC", "/tmp/rigsmith-worktrees/feat-x")
	if got := sourceLocation(); got != "/tmp/rigsmith-worktrees/feat-x" {
		t.Errorf("sourceLocation() = %q, want the RIGSMITH_DEV_SRC path", got)
	}
}

// Without it, the location falls back to the binary's own path (never empty in a
// normal run).
func TestSourceLocationFallsBackToExe(t *testing.T) {
	t.Setenv("RIGSMITH_DEV_SRC", "")
	if got := sourceLocation(); got == "" {
		t.Error("sourceLocation() should fall back to the executable path")
	}
}

// The "dev" ldflags sentinel is treated as unversioned, so a tool passing it
// (clauderig does) gets the from-source description, not a bare "dev".
func TestBuildVersionTreatsDevAsUnversioned(t *testing.T) {
	if got := buildVersion(settings{version: "dev"}); !strings.HasPrefix(got, "source build") {
		t.Errorf("buildVersion(dev) = %q, want the from-source description", got)
	}
}

// A versionless build is described, not left as a bare "unknown": it names the
// build and its source location (here, the dev worktree).
func TestSourceBuildVersionDescribesTheBuild(t *testing.T) {
	t.Setenv("RIGSMITH_DEV_SRC", "/tmp/rigsmith-worktrees/feat-x")
	got := sourceBuildVersion(nil)
	if !strings.HasPrefix(got, "source build") {
		t.Errorf("version = %q, want it to start with \"source build\"", got)
	}
	if !strings.Contains(got, "/tmp/rigsmith-worktrees/feat-x") {
		t.Errorf("version = %q, want it to include the source location", got)
	}
}

func TestPlainVersion(t *testing.T) {
	for in, want := range map[string]string{
		"1.20.3":            "1.20.3",
		"v1.20.3 (abc1234)": "1.20.3",
		"1.21.0-beta.1":     "1.21.0-beta.1",
		"source build · x":  "source build · x",
		"":                  "",
	} {
		if got := plainVersion(in); got != want {
			t.Errorf("plainVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

// In a terminal, --version shows the banner with the resolved version.
func TestVersionInATerminalShowsTheBanner(t *testing.T) {
	was := isTerminal
	isTerminal = func(io.Writer) bool { return true }
	t.Cleanup(func() { isTerminal = was })

	var buf bytes.Buffer
	root := &cobra.Command{Use: "demo", Run: func(*cobra.Command, []string) {}}
	root.SetOut(&buf)
	root.SetArgs([]string{"--version"})
	err := Execute(context.Background(), root,
		WithVersion("1.2.3"),
		WithBanner(func(v string) string { return "BANNER " + v }))
	if err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); got != "BANNER 1.2.3\n" {
		t.Errorf("--version in a terminal = %q, want the banner", got)
	}
}

// Text with template syntax in it is printed as it is: a source build's path
// can hold "{{", which as template source would panic cobra.
func TestVersionTemplatePrintsTemplateSyntaxLiterally(t *testing.T) {
	for _, tty := range []bool{false, true} {
		was := isTerminal
		isTerminal = func(io.Writer) bool { return tty }
		t.Cleanup(func() { isTerminal = was })

		var buf bytes.Buffer
		root := &cobra.Command{Use: "demo", Run: func(*cobra.Command, []string) {}}
		root.SetOut(&buf)
		root.SetArgs([]string{"--version"})
		err := Execute(context.Background(), root,
			WithVersion("1.2.3"),
			WithBanner(func(v string) string { return "BANNER {{ .Oops " + v }))
		if err != nil {
			t.Fatal(err)
		}
		want := "1.2.3\n"
		if tty {
			want = "BANNER {{ .Oops 1.2.3\n"
		}
		if got := buf.String(); got != want {
			t.Errorf("tty=%v: --version = %q, want %q", tty, got, want)
		}
	}
	if got := literalTemplate(`source build · /tmp/{{x}}/"q"`); !strings.HasPrefix(got, "{{") {
		t.Errorf("literalTemplate = %q", got)
	}
}

// Piped, a source build prints its description, path and all, even when the
// path holds template syntax.
func TestPipedSourceBuildVersionWithTemplateSyntax(t *testing.T) {
	t.Setenv("RIGSMITH_DEV_SRC", "/tmp/{{ .Oops }}/wt")
	was := isTerminal
	isTerminal = func(io.Writer) bool { return false }
	t.Cleanup(func() { isTerminal = was })

	var buf bytes.Buffer
	root := &cobra.Command{Use: "demo", Run: func(*cobra.Command, []string) {}}
	root.SetOut(&buf)
	root.SetArgs([]string{"--version"})
	if err := Execute(context.Background(), root, WithVersion("dev")); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); !strings.Contains(got, "/tmp/{{ .Oops }}/wt") {
		t.Errorf("--version = %q, want the source path as it is", got)
	}
}
