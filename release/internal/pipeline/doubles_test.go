// Test doubles ported from net-changesets Release/TestDoubles.cs: a recording
// runner, a recording reporter, and a stub prompter, so no real processes run.
package pipeline

import "strings"

// recordedCommand is one runner invocation. For a shell command args holds
// the single command line; for an argv command it is the argv.
type recordedCommand struct {
	shell bool
	args  []string
}

// line renders the call the way the C# tests render executor calls: shell
// commands as "sh -c <command>", argv commands joined with spaces.
func (c recordedCommand) line() string {
	if c.shell {
		return "sh -c " + c.args[0]
	}
	return strings.Join(c.args, " ")
}

// hasArg reports whether an argv element equals token (always false for shell
// commands, mirroring the C# Argv.Contains checks).
func (c recordedCommand) hasArg(token string) bool {
	if c.shell {
		return false
	}
	for _, arg := range c.args {
		if arg == token {
			return true
		}
	}
	return false
}

// recordingRunner records every invocation and returns a configurable result,
// with no real process started.
type recordingRunner struct {
	calls []recordedCommand

	// responder decides the result for a recorded command; defaults to
	// success when not set.
	responder func(recordedCommand) ([]string, int)
}

func (r *recordingRunner) run(shell bool, commandOrArgv []string, dir string) ([]string, int, error) {
	call := recordedCommand{shell: shell, args: append([]string(nil), commandOrArgv...)}
	r.calls = append(r.calls, call)
	if r.responder != nil {
		output, exitCode := r.responder(call)
		return output, exitCode, nil
	}
	return nil, 0, nil
}

func (r *recordingRunner) lines() []string {
	lines := make([]string, len(r.calls))
	for i, call := range r.calls {
		lines[i] = call.line()
	}
	return lines
}

// skippedStep records a StepSkipped event.
type skippedStep struct {
	name   string
	reason string
}

// recordingReporter captures pipeline events for assertions.
type recordingReporter struct {
	plannedSteps  []ResolvedStep
	plannedDryRun *bool
	startedSteps  []string
	skippedSteps  []skippedStep
	completed     []string
	cancelledStep string
	success       *bool
	message       string
}

func (r *recordingReporter) Plan(steps []ResolvedStep, dryRun bool) {
	r.plannedSteps = steps
	r.plannedDryRun = &dryRun
}

func (r *recordingReporter) StepStarted(name string) { r.startedSteps = append(r.startedSteps, name) }

func (r *recordingReporter) StepSkipped(name, reason string) {
	r.skippedSteps = append(r.skippedSteps, skippedStep{name, reason})
}

func (r *recordingReporter) CommandStarted(string, CommandSpec) {}

func (r *recordingReporter) CommandOutput([]string) {}

func (r *recordingReporter) CommandFailed(string, int) {}

func (r *recordingReporter) StepCompleted(name string) { r.completed = append(r.completed, name) }

func (r *recordingReporter) StepCancelled(name string) { r.cancelledStep = name }

func (r *recordingReporter) RunCompleted(success bool, message string) {
	r.success = &success
	r.message = message
}

// stubPrompter records the prompts it is asked and answers with a
// configurable, fixed response.
type stubPrompter struct {
	prompts []string
	answer  bool
}

func (p *stubPrompter) Confirm(message string) bool {
	p.prompts = append(p.prompts, message)
	return p.answer
}
