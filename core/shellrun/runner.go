package shellrun

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"runtime"
	"strings"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

// Runner is the exec seam: it runs one external command and returns its
// combined output lines and exit code. When shell is true, commandOrArgv has
// exactly one element — the shell command line, to be dispatched through a
// shell; otherwise commandOrArgv is the argv, with commandOrArgv[0] the
// executable, each token passed verbatim with no shell. A non-nil err means
// the command could not be run at all.
type Runner func(shell bool, commandOrArgv []string, dir string) (output []string, exitCode int, err error)

// ExecRunner is a production Runner, running commands with os/exec and the
// ambient process environment. Shell commands go through /bin/sh -c (cmd.exe /c
// on Windows); argv commands are exec'd directly. Stdout and stderr are merged,
// as callers report a single output stream.
func ExecRunner(shell bool, commandOrArgv []string, dir string) ([]string, int, error) {
	return runExec(nil, shell, commandOrArgv, dir)
}

// NewExecRunner returns a production Runner that runs each command with env as
// its environment (in "KEY=VALUE" form; nil or empty inherits the ambient
// process environment). The caller wires its layered .env/.env.local < ambient
// stack in here, so spawned commands and variable captures see the same
// environment as the host.
func NewExecRunner(env []string) Runner {
	// Normalise empty (non-nil) to nil so "empty inherits" holds, matching
	// NewPortableRunner — otherwise exec.Cmd would run with a cleared
	// environment (no PATH). envstack.Environ of an empty map yields exactly
	// this empty (non-nil) slice, which is the easy way to trip over it.
	if len(env) == 0 {
		env = nil
	}
	return func(shell bool, commandOrArgv []string, dir string) ([]string, int, error) {
		return runExec(env, shell, commandOrArgv, dir)
	}
}

func runExec(env []string, shell bool, commandOrArgv []string, dir string) ([]string, int, error) {
	var cmd *exec.Cmd
	if shell {
		shellExe, flag := "/bin/sh", "-c"
		if runtime.GOOS == "windows" {
			shellExe, flag = "cmd.exe", "/c"
		}
		cmd = exec.Command(shellExe, flag, commandOrArgv[0])
	} else {
		cmd = exec.Command(commandOrArgv[0], commandOrArgv[1:]...)
	}
	cmd.Dir = dir
	cmd.Env = env // nil inherits the current process environment

	combined, err := cmd.CombinedOutput()
	lines := splitOutputLines(combined)
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return lines, exitErr.ExitCode(), nil
		}
		return lines, -1, err
	}
	return lines, 0, nil
}

func splitOutputLines(output []byte) []string {
	text := strings.TrimRight(string(output), "\n")
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSuffix(line, "\r")
	}
	return lines
}

// NewPortableRunner returns a Runner whose shell commands are executed by an
// in-process, mostly-bash-compatible shell interpreter (mvdan.cc/sh) instead of
// the OS shell, so the same script — pipes, &&/||, $VAR, globbing, if/for,
// [[ … ]], arrays — runs identically on Linux, macOS, and Windows without a real
// /bin/sh or cmd.exe. argv commands are exec'd directly, exactly like
// NewExecRunner (they have no shell syntax to normalise). env is the layered
// environment in KEY=VALUE form; nil or empty inherits the process environment.
//
// It provides cross-platform cp/mv/rm/mkdir builtins (see portableFileOps) so the
// common file operations a script needs work everywhere; other external commands
// (git, npm, gh) resolve from PATH like a shell does. It does not ship a full
// Unix userland, so a script that calls e.g. `sed` still needs `sed` on the
// host — that (plus a rare unsupported construct) is what the system-shell
// escape hatch is for.
func NewPortableRunner(env []string) Runner {
	// Normalise an empty (non-nil) env to nil so "empty inherits the process
	// environment" holds for argv commands too — otherwise exec.Cmd would run
	// them with a cleared environment (no PATH).
	if len(env) == 0 {
		env = nil
	}
	return func(shell bool, commandOrArgv []string, dir string) ([]string, int, error) {
		if !shell {
			return runExec(env, false, commandOrArgv, dir)
		}
		return runPortableShell(env, commandOrArgv[0], dir)
	}
}

func runPortableShell(env []string, script, dir string) ([]string, int, error) {
	file, err := syntax.NewParser().Parse(strings.NewReader(script), "")
	if err != nil {
		return nil, -1, err
	}

	var out bytes.Buffer // stdout+stderr merged, matching the system runner
	opts := []interp.RunnerOption{
		interp.Dir(dir),
		interp.StdIO(nil, &out, &out),
		// Cross-platform cp/mv/rm/mkdir in Go; everything else falls through to
		// the default exec handler (git, npm, gh, …).
		interp.ExecHandlers(portableFileOps),
	}
	// A populated env replaces the process environment exactly (the host already
	// merged ambient into it); nil/empty falls back to the interpreter default,
	// which is the process environment.
	if len(env) > 0 {
		opts = append(opts, interp.Env(expand.ListEnviron(env...)))
	}

	runner, err := interp.New(opts...)
	if err != nil {
		return nil, -1, err
	}

	runErr := runner.Run(context.Background(), file)
	lines := splitOutputLines(out.Bytes())
	if runErr != nil {
		if status, ok := interp.IsExitStatus(runErr); ok {
			return lines, int(status), nil
		}
		return lines, -1, runErr
	}
	return lines, 0, nil
}

// RunPortable runs a shell command line through the in-process portable shell,
// streaming stdin/stdout/stderr live like an interactive OS shell instead of
// capturing output. It is the entry point for commands run on a user's behalf
// (e.g. rig custom commands), where live output, stdin, and ctx cancellation
// matter — unlike NewPortableRunner, which buffers output for after-the-fact
// reporting. The same cross-platform cp/mv/rm/mkdir builtins apply.
//
// dir is the working directory; env is the command environment in KEY=VALUE
// form (nil/empty inherits the process environment). Returns the command's exit
// code; a non-nil error means the script could not be parsed or run at all
// (a non-zero exit is reported as the code, not an error).
func RunPortable(ctx context.Context, line, dir string, env []string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	file, err := syntax.NewParser().Parse(strings.NewReader(line), "")
	if err != nil {
		return -1, err
	}

	opts := []interp.RunnerOption{
		interp.Dir(dir),
		interp.StdIO(stdin, stdout, stderr),
		interp.ExecHandlers(portableFileOps),
	}
	if len(env) > 0 {
		opts = append(opts, interp.Env(expand.ListEnviron(env...)))
	}

	runner, err := interp.New(opts...)
	if err != nil {
		return -1, err
	}

	runErr := runner.Run(ctx, file)
	if runErr != nil {
		if status, ok := interp.IsExitStatus(runErr); ok {
			return int(status), nil
		}
		return -1, runErr
	}
	return 0, nil
}
