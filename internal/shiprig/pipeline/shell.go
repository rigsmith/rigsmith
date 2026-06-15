package pipeline

import (
	"bytes"
	"context"
	"strings"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

// NewPortableRunner returns a Runner whose shell commands are executed by an
// in-process, mostly-bash-compatible shell interpreter (mvdan.cc/sh) instead of
// the OS shell, so the same script — pipes, &&/||, $VAR, globbing, if/for,
// [[ … ]], arrays — runs identically on Linux, macOS, and Windows without a real
// /bin/sh or cmd.exe. argv commands are exec'd directly, exactly like
// NewExecRunner (they have no shell syntax to normalise). env is the layered
// release environment in KEY=VALUE form; nil or empty inherits the process
// environment.
//
// It provides cross-platform cp/mv/rm/mkdir builtins (see portableFileOps) so the
// common file operations a release step needs work everywhere; other external
// commands (git, npm, gh) resolve from PATH like a shell does. It does not ship a
// full Unix userland, so a script that calls e.g. `sed` still needs `sed` on the
// host — that (plus a rare unsupported construct) is what the "shell": "system"
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
