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

// ExecRunner is the production Runner, running commands with os/exec and the
// ambient process environment. Shell commands go through /bin/sh -c (cmd.exe /c
// on Windows); argv commands are exec'd directly. Stdout and stderr are merged,
// as the pipeline reports a single output stream.
func ExecRunner(shell bool, commandOrArgv []string, dir string) ([]string, int, error) {
	return runExec(nil, shell, commandOrArgv, dir)
}

// NewExecRunner returns a production Runner that runs each command with env as
// its environment (in "KEY=VALUE" form; nil inherits the ambient process
// environment). The release command wires the layered .env/.env.local < ambient
// stack in here, so spawned release steps and variable captures see the same
// environment as ${env.NAME} interpolation.
func NewExecRunner(env []string) Runner {
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
	env      map[string]string
	native   map[string]NativeHandler

	vars        *variables
	release     *releaseVars
	relctx      ReleaseContext
	baseContext map[string]string

	// scriptCtx is the `ctx` object exposed to Tengo `if` conditions and computed
	// vars; built once per run.
	scriptCtx map[string]interface{}
	// scriptDryRun previews a script step's side effects (sh/cp/…) instead of
	// executing them.
	scriptDryRun bool
}

// New builds a Pipeline. env is the layered release environment
// (.env/.env.local < ambient) used to resolve ${env.NAME} placeholders; nil
// falls back to the process environment. nativeSteps maps native step names
// (e.g. "release") to host-registered handlers; it may be nil. relctx supplies
// the built-in release variables (${version.*}, ${versions}, ${releaseUrl.*},
// ${issues}, …); nil leaves those placeholders unresolved (verbatim).
func New(
	runner Runner,
	reporter Reporter,
	masker *SecretMasker,
	prompter Prompter,
	workDir string,
	env map[string]string,
	nativeSteps map[string]NativeHandler,
	relctx ReleaseContext,
) *Pipeline {
	return &Pipeline{
		runner:   runner,
		reporter: reporter,
		masker:   masker,
		prompter: prompter,
		workDir:  workDir,
		env:      env,
		native:   nativeSteps,
		release:  newReleaseVars(relctx),
		relctx:   relctx,
	}
}

// Run executes the resolved steps. A dry run reports the interpolated plan and
// executes only the commands a step opts into via "dryRun" (no ${vars.*} are
// ever captured); see runDry. Returns true when the whole run (including global
// hooks) succeeds.
func (p *Pipeline) Run(steps []ResolvedStep, config *Config, dryRun bool) bool {
	p.baseContext = map[string]string{"tool": toolOf(config)}
	p.scriptCtx = buildScriptCtx(p.relctx, p.env, dryRun)
	p.scriptDryRun = dryRun
	scriptEval := func(expr string) (string, error) { return evalScriptString(expr, p.scriptCtx) }
	p.vars = newVariables(config.Vars, p.runner, p.masker, p.workDir, scriptEval)

	if dryRun {
		return p.runDry(steps)
	}

	p.reporter.Plan(steps, false)

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

		run, reason, ok := p.evalStepIf(step)
		if !ok {
			return p.fail(hooks, fmt.Sprintf("step '%s' if-condition error: %s", step.Name, reason))
		}
		if !run {
			p.reporter.StepSkipped(step.Name, reason)
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

// runDry renders the interpolated plan (Part A: built-in variables filled in,
// ${vars.*} and ${releaseUrl*} shown as placeholders, nothing captured) and then
// executes only the commands a step opted into with "dryRun" (Part B). Confirm
// gates are not prompted and native handlers never fire — a native step runs in
// a dry run only if it carries explicit "dryRun" commands.
func (p *Pipeline) runDry(steps []ResolvedStep) bool {
	p.reporter.Plan(p.previewSteps(steps), true)

	for _, step := range steps {
		if !step.Enabled() {
			continue
		}
		if run, _, ok := p.evalStepIf(step); !ok || !run {
			continue // an if-false (or erroring) step runs nothing
		}

		// A script step runs in a dry run (its logic executes; sh/cp/… are
		// previewed). A command step runs only the commands it opted in via
		// "dryRun". Everything else is preview-only.
		var ran bool
		switch {
		case step.Kind == StepKindScript:
			ran = true
			p.reporter.StepStarted(step.Name)
			if !p.runScriptStep(step) {
				p.reporter.RunCompleted(false, fmt.Sprintf("dry run: step '%s' failed", step.Name))
				return false
			}
		case len(step.DryRunAction) > 0:
			ran = true
			p.reporter.StepStarted(step.Name)
			if !p.runDryCommands(step.Name+" (dry-run)", step.DryRunAction) {
				p.reporter.RunCompleted(false, fmt.Sprintf("dry run: step '%s' failed", step.Name))
				return false
			}
		}
		if ran {
			p.reporter.StepCompleted(step.Name)
		}
	}

	p.reporter.RunCompleted(true, "dry run - plan previewed, only dryRun-marked commands ran")
	return true
}

// evalStepIf evaluates a step's `if` gate against the script ctx. It returns
// whether the step should run, a skip/error reason, and ok=false only when the
// expression itself errored.
func (p *Pipeline) evalStepIf(step ResolvedStep) (shouldRun bool, reason string, ok bool) {
	if step.If == "" {
		return true, "", true
	}
	result, err := evalScriptBool(step.If, p.scriptCtx)
	if err != nil {
		return false, err.Error(), false
	}
	if !result {
		return false, "if condition is false", true
	}
	return true, "", true
}

// previewSteps returns copies of the steps with before/after/action commands
// interpolated for display, the action hidden when "dryRun": false, and an
// if-false step marked skipped so the dry-run plan is accurate.
func (p *Pipeline) previewSteps(steps []ResolvedStep) []ResolvedStep {
	out := make([]ResolvedStep, len(steps))
	for i, s := range steps {
		if s.Enabled() {
			if run, reason, ok := p.evalStepIf(s); ok && !run {
				s.SkipReason = reason
			}
		}
		s.Before = p.previewCommands(s.Before)
		s.After = p.previewCommands(s.After)
		if s.DryRunHidden {
			s.Action = nil
		} else {
			s.Action = p.previewCommands(s.Action)
		}
		out[i] = s
	}
	return out
}

func (p *Pipeline) previewCommands(commands []CommandSpec) []CommandSpec {
	if commands == nil {
		return nil
	}
	out := make([]CommandSpec, len(commands))
	for i, c := range commands {
		out[i] = p.previewInterpolate(c)
	}
	return out
}

// runDryCommands runs opted-in dry-run commands. Like previewInterpolate it
// never captures ${vars.*} (placeholdered) and never resolves forge URLs, so a
// dry run has no side effects beyond the command the user marked safe.
func (p *Pipeline) runDryCommands(label string, commands []CommandSpec) bool {
	for _, command := range commands {
		resolved := p.previewInterpolate(command)
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

// previewInterpolate fills a command's placeholders for a dry run: built-in
// release variables (versions/tags/changelog/issues) resolve to their planned
// values; ${vars.*} and ${releaseUrl*} become ‹…› placeholders (resolving them
// has side effects or is impossible before the release runs); ${env.*}/${tool}
// resolve normally. A release usage error (ambiguous bare form, unknown package)
// is left verbatim so the unresolved reference stays visible.
func (p *Pipeline) previewInterpolate(command CommandSpec) CommandSpec {
	context := make(map[string]string, len(p.baseContext)+4)
	for key, value := range p.baseContext {
		context[key] = value
	}

	for _, key := range extractRefs(command) {
		if name, ok := strings.CutPrefix(key, "vars."); ok {
			// A literal var is knowable with no side effect, so show it; a
			// captured var would run its command, so placeholder it.
			if p.vars != nil {
				if value, known := p.vars.previewValue(name); known {
					context[key] = value
					continue
				}
			}
			context[key] = "‹" + key + "›"
			continue
		}
		if isReleaseURLKey(key) {
			context[key] = "‹" + key + "›"
			continue
		}
		if p.release != nil {
			if value, isRelease, err := p.release.resolve(key); isRelease && err == nil {
				context[key] = value
			}
		}
	}

	return interpolateCommand(context, p.env, command)
}

func (p *Pipeline) runAction(step ResolvedStep) bool {
	switch step.Kind {
	case StepKindScript:
		return p.runScriptStep(step)
	case StepKindCommands:
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

		// Built-in release variables (${version.*}, ${versions}, ${releaseUrl.*},
		// ${issues}, …). Resolved per command so forge URLs reflect whether the
		// release step has run yet. A usage error (ambiguous bare form, unknown
		// package/issue) fails the command with its guidance message.
		if p.release != nil {
			for _, key := range extractRefs(command) {
				value, isRelease, err := p.release.resolve(key)
				if !isRelease {
					continue
				}
				if err != nil {
					p.reporter.CommandOutput([]string{err.Error()})
					p.reporter.CommandFailed(fmt.Sprintf("%s (${%s})", label, key), -1)
					return false
				}
				context[key] = value
			}
		}

		resolved := interpolateCommand(context, p.env, command)
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
