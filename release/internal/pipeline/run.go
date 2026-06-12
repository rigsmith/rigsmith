package pipeline

import (
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// Runner is the exec seam: it runs one external command and returns its
// combined output lines and exit code. When shell is true, commandOrArgv has
// exactly one element — the shell command line, to be dispatched through the
// OS shell (sh -c / cmd.exe /c); otherwise commandOrArgv is the argv, with
// commandOrArgv[0] the executable, each token passed verbatim with no shell.
// A non-nil err means the command could not be run at all.
type Runner func(shell bool, commandOrArgv []string, dir string) (output []string, exitCode int, err error)

// ExecRunner is the production Runner, running commands with os/exec. Shell
// commands go through /bin/sh -c (cmd.exe /c on Windows); argv commands are
// exec'd directly. Stdout and stderr are merged, as the pipeline reports a
// single output stream.
func ExecRunner(shell bool, commandOrArgv []string, dir string) ([]string, int, error) {
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

// dispatch launches a CommandSpec through the runner: shell commands via the
// OS shell, argv commands exec'd directly. Shared by the pipeline and by
// variable capture so both dispatch identically. A runner error is folded
// into the output and a non-zero exit code.
func dispatch(runner Runner, command CommandSpec, dir string) ([]string, int) {
	var (
		output []string
		code   int
		err    error
	)
	if command.IsShell() {
		output, code, err = runner(true, []string{command.Shell()}, dir)
	} else {
		output, code, err = runner(false, command.Argv(), dir)
	}
	if err != nil {
		output = append(output, err.Error())
		if code == 0 {
			code = -1
		}
	}
	return output, code
}

// NativeHandler carries out a native step (e.g. per-package forge releases),
// returning false to fail the release.
type NativeHandler func() bool

// Pipeline runs resolved steps, reporting progress through a Reporter.
type Pipeline struct {
	runner   Runner
	reporter Reporter
	masker   *SecretMasker
	prompter Prompter
	workDir  string
	native   map[string]NativeHandler

	vars        *variables
	baseContext map[string]string
}

// New builds a Pipeline. nativeSteps maps native step names (e.g.
// "githubRelease") to host-registered handlers; it may be nil.
func New(
	runner Runner,
	reporter Reporter,
	masker *SecretMasker,
	prompter Prompter,
	workDir string,
	nativeSteps map[string]NativeHandler,
) *Pipeline {
	return &Pipeline{
		runner:   runner,
		reporter: reporter,
		masker:   masker,
		prompter: prompter,
		workDir:  workDir,
		native:   nativeSteps,
	}
}

// Run executes the resolved steps. In a dry run nothing is executed (and no
// vars are captured) — the plan is reported and Run returns true. Returns
// true when the whole run (including global hooks) succeeds.
func (p *Pipeline) Run(steps []ResolvedStep, config *Config, dryRun bool) bool {
	p.reporter.Plan(steps, dryRun)

	if dryRun {
		p.reporter.RunCompleted(true, "dry run - nothing executed")
		return true
	}

	p.baseContext = map[string]string{"tool": toolOf(config)}
	p.vars = newVariables(config.Vars, p.runner, p.masker, p.workDir)

	var hooks Hooks
	if config.Hooks != nil {
		hooks = *config.Hooks
	}

	if !p.runCommands("hooks (before)", hooks.Before) {
		return p.fail(hooks, "global before hook failed")
	}

	// Eagerly capture the non-lazy variables up front so a broken capture
	// command fails fast.
	for _, name := range p.vars.eagerNames() {
		resolution := p.vars.resolve(name)
		if !resolution.ok {
			message := resolution.err
			if message == "" {
				message = fmt.Sprintf("variable '%s' failed", name)
			}
			return p.fail(hooks, message)
		}
	}

	for _, step := range steps {
		if !step.Enabled() {
			p.reporter.StepSkipped(step.Name, step.SkipReason)
			continue
		}

		p.reporter.StepStarted(step.Name)

		if !p.runCommands(step.Name+" (before)", step.Before) {
			return p.fail(hooks, fmt.Sprintf("step '%s' failed", step.Name))
		}

		// Confirmation gate: after `before` hooks have run (so tests/build
		// inform the decision), before the consequential action. Declining
		// stops the run without treating it as a failure.
		if step.Confirm != nil && !p.prompter.Confirm(*step.Confirm) {
			p.reporter.StepCancelled(step.Name)
			return false
		}

		if !p.runAction(step) || !p.runCommands(step.Name+" (after)", step.After) {
			return p.fail(hooks, fmt.Sprintf("step '%s' failed", step.Name))
		}

		p.reporter.StepCompleted(step.Name)
	}

	if !p.runCommands("hooks (after)", hooks.After) {
		return p.fail(hooks, "global after hook failed")
	}

	p.reporter.RunCompleted(true, "")
	return true
}

func (p *Pipeline) fail(hooks Hooks, message string) bool {
	// Best-effort cleanup: run onError but don't let its own failure mask the
	// original one.
	p.runCommands("hooks (on-error)", hooks.OnError)
	p.reporter.RunCompleted(false, message)
	return false
}

func (p *Pipeline) runAction(step ResolvedStep) bool {
	if step.Kind != StepKindNative {
		return p.runCommands(step.Name, step.Action)
	}

	handler, ok := p.native[step.Name]
	if !ok {
		// No host handler wired (e.g. a minimal pipeline); treat as a no-op
		// rather than failing.
		p.reporter.StepSkipped(step.Name, "no handler")
		return true
	}

	return handler()
}

func (p *Pipeline) runCommands(label string, commands []CommandSpec) bool {
	for _, command := range commands {
		context := make(map[string]string, len(p.baseContext)+2)
		for key, value := range p.baseContext {
			context[key] = value
		}

		for _, name := range extractVarRefs(command) {
			resolution := p.vars.resolve(name)
			if !resolution.ok {
				p.reporter.CommandFailed(fmt.Sprintf("%s (vars.%s)", label, name), resolution.exitCode)
				return false
			}
			context["vars."+name] = resolution.value
		}

		resolved := interpolateCommand(context, command)
		p.reporter.CommandStarted(label, resolved)

		output, exitCode := dispatch(p.runner, resolved, p.workDir)
		p.reporter.CommandOutput(output)

		if exitCode != 0 {
			p.reporter.CommandFailed(label, exitCode)
			return false
		}
	}

	return true
}
