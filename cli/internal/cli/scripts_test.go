// Port of the .NET rig's IntegrationTests for custom commands: spawn a real
// process through runCustom and verify the wiring — exit-code propagation for
// the shell and argv forms, passthrough-arg forwarding, per-command env, and
// the clean missing-OS-spec error. The .NET suite's real-`dotnet`-build E2E is
// intentionally not ported: Go's build verb is the ecosystem-generic runner
// (covered by unit tests) and spawning the SDK would dominate the suite's
// runtime. The shell-form cases use `sh`, so they are skipped on Windows
// (runCustom's shell form is `sh -c` there too — a known limitation).
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
func newRunHost() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	return cmd
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

func skipOnWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("custom shell commands run via `sh -c`, unavailable on Windows")
	}
}

func TestCustomShellCommand_PropagatesTheExitCode(t *testing.T) {
	skipOnWindows(t)
	def := &config.Command{Spec: &config.CommandSpec{Shell: "exit 3"}}

	err := runCustom(newRunHost(), config.Config{}, t.TempDir(), "boom", def, nil)

	if got := exitCode(t, err); got != 3 {
		t.Fatalf("exit code = %d, want 3 (a custom shell command's exit code becomes rig's)", got)
	}
}

func TestCustomShellCommand_AppendsPassthroughArgs(t *testing.T) {
	skipOnWindows(t)
	// `exit` + passthrough `4` → the shell runs `exit 4`
	def := &config.Command{Spec: &config.CommandSpec{Shell: "exit"}}

	err := runCustom(newRunHost(), config.Config{}, t.TempDir(), "code", def, []string{"4"})

	if got := exitCode(t, err); got != 4 {
		t.Fatalf("exit code = %d, want 4", got)
	}
}

func TestCustomArgvCommand_ExecsDirectlyAndPropagatesExitCode(t *testing.T) {
	skipOnWindows(t)
	def := &config.Command{Spec: &config.CommandSpec{Argv: []string{"sh", "-c", "exit 5"}}}

	err := runCustom(newRunHost(), config.Config{}, t.TempDir(), "x", def, nil)

	if got := exitCode(t, err); got != 5 {
		t.Fatalf("exit code = %d, want 5 (argv form bypasses the shell yet still propagates)", got)
	}
}

func TestCustomCommandEnv_ReachesTheChildProcess(t *testing.T) {
	skipOnWindows(t)
	// exits with the value of an env var rig injects
	def := &config.Command{
		Spec: &config.CommandSpec{Argv: []string{"sh", "-c", "exit $RIG_TC"}},
		Env:  map[string]string{"RIG_TC": "6"},
	}

	err := runCustom(newRunHost(), config.Config{}, t.TempDir(), "x", def, nil)

	if got := exitCode(t, err); got != 6 {
		t.Fatalf("exit code = %d, want 6 (per-command env must reach the child)", got)
	}
}

func TestCustomCommandWithNoSpecForThisOS_ErrorsCleanly(t *testing.T) {
	def := &config.Command{OS: map[string]*config.CommandSpec{"plan9": {Shell: "true"}}}

	err := runCustom(newRunHost(), config.Config{}, t.TempDir(), "x", def, nil)

	if err == nil || !strings.Contains(err.Error(), "no command defined for this OS") {
		t.Fatalf("err = %v, want a clean no-spec-for-this-OS error", err)
	}
}
