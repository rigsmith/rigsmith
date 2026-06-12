// Port of the .NET rig's IntegrationTests for custom commands: spawn a real
// process through runCustom and verify the wiring — exit-code propagation for
// the shell and argv forms, passthrough-arg forwarding, per-command env, and
// the clean missing-OS-spec error. The .NET suite's real-`dotnet`-build E2E is
// intentionally not ported: Go's build verb is the ecosystem-generic runner
// (covered by unit tests) and spawning the SDK would dominate the suite's
// runtime. The shell form is OS-native (sh -c / cmd.exe /c), so these run on
// Windows too, with cmd-flavored fixtures where syntax differs.
package cli

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"github.com/rigsmith/cli/internal/config"
	"github.com/spf13/cobra"
)

// newRunHost builds a bare command to host runCustom (output captured).
func newRunHost() (*cobra.Command, *bytes.Buffer) {
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	return cmd, &buf
}

// exitCode extracts the child's exit code from runCustom's error (0 on nil).
func exitCode(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("want an *exec.ExitError, got %T: %v", err, err)
	}
	return ee.ExitCode()
}

// shArgv builds an argv-form fixture that exits with code via the OS shell.
func shArgv(script, winScript string) []string {
	if runtime.GOOS == "windows" {
		return []string{"cmd.exe", "/d", "/s", "/c", winScript}
	}
	return []string{"sh", "-c", script}
}

func TestCustomShellCommand_PropagatesTheExitCode(t *testing.T) {
	// `exit 3` is valid in both sh and cmd.
	def := &config.Command{Spec: &config.CommandSpec{Shell: "exit 3"}}

	host, _ := newRunHost()
	err := runCustom(host, config.Config{}, t.TempDir(), "boom", def, nil)

	if got := exitCode(t, err); got != 3 {
		t.Fatalf("exit code = %d, want 3 (a custom shell command's exit code becomes rig's)", got)
	}
}

func TestCustomShellCommand_AppendsPassthroughArgs(t *testing.T) {
	// The passthrough arg must reach the shell line. Quoting differs per OS
	// (posix single-quote vs cmd caret-escape), so assert via echoed output.
	def := &config.Command{Spec: &config.CommandSpec{Shell: "echo rig-arg:"}}

	host, buf := newRunHost()
	err := runCustom(host, config.Config{}, t.TempDir(), "say", def, []string{"hello-passthrough"})

	if got := exitCode(t, err); got != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", got, buf.String())
	}
	if !strings.Contains(buf.String(), "hello-passthrough") {
		t.Fatalf("output missing the forwarded arg:\n%s", buf.String())
	}
}

func TestCustomArgvCommand_ExecsDirectlyAndPropagatesExitCode(t *testing.T) {
	def := &config.Command{Spec: &config.CommandSpec{Argv: shArgv("exit 5", "exit 5")}}

	host, _ := newRunHost()
	err := runCustom(host, config.Config{}, t.TempDir(), "x", def, nil)

	if got := exitCode(t, err); got != 5 {
		t.Fatalf("exit code = %d, want 5 (argv form bypasses the shell yet still propagates)", got)
	}
}

func TestCustomCommandEnv_ReachesTheChildProcess(t *testing.T) {
	// exits with the value of an env var rig injects ($VAR in sh, %VAR% in cmd)
	def := &config.Command{
		Spec: &config.CommandSpec{Argv: shArgv("exit $RIG_TC", "exit %RIG_TC%")},
		Env:  map[string]string{"RIG_TC": "6"},
	}

	host, _ := newRunHost()
	err := runCustom(host, config.Config{}, t.TempDir(), "x", def, nil)

	if got := exitCode(t, err); got != 6 {
		t.Fatalf("exit code = %d, want 6 (per-command env must reach the child)", got)
	}
}

func TestCustomCommandWithNoSpecForThisOS_ErrorsCleanly(t *testing.T) {
	def := &config.Command{OS: map[string]*config.CommandSpec{"plan9": {Shell: "true"}}}

	host, _ := newRunHost()
	err := runCustom(host, config.Config{}, t.TempDir(), "x", def, nil)

	if err == nil || !strings.Contains(err.Error(), "no command defined for this OS") {
		t.Fatalf("err = %v, want a clean no-spec-for-this-OS error", err)
	}
}

func TestShellInvocationShapes(t *testing.T) {
	display, argv := shellInvocation("echo hi", []string{"a b"})
	if runtime.GOOS == "windows" {
		if argv[0] != "cmd.exe" || argv[1] != "/d" || argv[3] != "/c" {
			t.Fatalf("windows argv = %v, want cmd.exe /d /s /c", argv)
		}
		// The forwarded arg is appended caret-escaped (winShellArg), so the
		// raw "a b" won't appear verbatim — the space is escaped to "a^ b".
		if want := "echo hi " + winShellArg("a b"); display != want {
			t.Fatalf("windows display = %q, want %q", display, want)
		}
		return
	}
	if argv[0] != "sh" || argv[1] != "-c" || argv[2] != "echo hi 'a b'" {
		t.Fatalf("posix argv = %v", argv)
	}
	if display != "echo hi 'a b'" {
		t.Fatalf("display = %q", display)
	}
}
