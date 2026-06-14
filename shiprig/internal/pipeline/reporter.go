package pipeline

import (
	"fmt"
	"io"
	"strings"
	"unicode"
)

// Reporter is the sink for pipeline progress events. The engine is headless
// and reports through this interface, so the same run can drive a plain-text
// renderer, an interactive TUI, or a test spy.
type Reporter interface {
	// Plan reports the resolved plan before execution (or as the whole output
	// of a dry run).
	Plan(steps []ResolvedStep, dryRun bool)

	// StepStarted reports that a step is about to run.
	StepStarted(name string)

	// StepSkipped reports that a step was skipped, with the reason (disabled,
	// filtered out, nothing to do).
	StepSkipped(name, reason string)

	// CommandStarted reports that a command is about to run; label locates it.
	CommandStarted(label string, command CommandSpec)

	// CommandOutput reports output captured from the most recent command.
	CommandOutput(lines []string)

	// CommandFailed reports a command that failed with a non-zero exit code.
	CommandFailed(label string, exitCode int)

	// StepCompleted reports that a step finished successfully.
	StepCompleted(name string)

	// StepCancelled reports that the user declined a confirmation gate, so
	// the run stops at this step.
	StepCancelled(name string)

	// RunCompleted reports that the whole run finished. message may be "".
	RunCompleted(success bool, message string)
}

// DescribeCommand renders a CommandSpec as a human-readable, single-line
// string for plans and logs: a shell command as-is; an argv joined with
// spaces, with empty or whitespace-containing tokens double-quoted so the
// rendering is unambiguous.
func DescribeCommand(command CommandSpec) string {
	if command.IsShell() {
		return command.Shell()
	}
	tokens := make([]string, len(command.Argv()))
	for i, token := range command.Argv() {
		tokens[i] = quoteToken(token)
	}
	return strings.Join(tokens, " ")
}

func quoteToken(token string) string {
	if token == "" || strings.ContainsFunc(token, unicode.IsSpace) {
		return `"` + token + `"`
	}
	return token
}

// PlainReporter is the line-oriented renderer for the release pipeline: no
// cursor control or styling, safe for redirected output and CI.
type PlainReporter struct {
	w           io.Writer
	masker      *SecretMasker
	tool        string
	currentStep string
}

// NewPlainReporter builds a PlainReporter writing to w. tool names the
// release binary in the resume hint; "" defaults to DefaultTool.
func NewPlainReporter(w io.Writer, masker *SecretMasker, tool string) *PlainReporter {
	if tool == "" {
		tool = DefaultTool
	}
	return &PlainReporter{w: w, masker: masker, tool: tool}
}

// Plan renders the resolved step list, with skip reasons for steps that will
// not run.
func (r *PlainReporter) Plan(steps []ResolvedStep, dryRun bool) {
	if dryRun {
		fmt.Fprintln(r.w, "Release plan (dry run - nothing will run):")
	} else {
		fmt.Fprintln(r.w, "Release plan:")
	}

	for _, step := range steps {
		if !step.Enabled() {
			fmt.Fprintf(r.w, "  - %s (%s)\n", step.Name, step.SkipReason)
			continue
		}

		fmt.Fprintf(r.w, "  - %s\n", step.Name)
		r.writePlanCommands("before", step.Before)
		if step.Confirm != nil {
			fmt.Fprintf(r.w, "      confirm: %s\n", *step.Confirm)
		}

		if step.Kind == StepKindNative {
			fmt.Fprintln(r.w, "      run: (per-package forge release)")
		} else if step.IsBuiltin {
			r.writePlanCommands("run", step.Action)
		} else {
			r.writePlanCommands("", step.Action)
		}

		r.writePlanCommands("after", step.After)
	}

	fmt.Fprintln(r.w)
}

func (r *PlainReporter) writePlanCommands(label string, commands []CommandSpec) {
	for _, command := range commands {
		prefix := ""
		if label != "" {
			prefix = label + ": "
		}
		fmt.Fprintf(r.w, "      %s%s\n", prefix, DescribeCommand(command))
	}
}

// StepStarted prints the step heading and remembers it for the resume hint.
func (r *PlainReporter) StepStarted(name string) {
	r.currentStep = name
	fmt.Fprintf(r.w, "==> %s\n", name)
}

// StepSkipped prints the skip line with its reason.
func (r *PlainReporter) StepSkipped(name, reason string) {
	fmt.Fprintf(r.w, "--- %s skipped (%s)\n", name, reason)
}

// CommandStarted prints the (masked) command line.
func (r *PlainReporter) CommandStarted(label string, command CommandSpec) {
	fmt.Fprintf(r.w, "    $ %s\n", r.masker.Mask(DescribeCommand(command)))
}

// CommandOutput prints the (masked) captured output, indented.
func (r *PlainReporter) CommandOutput(lines []string) {
	for _, line := range lines {
		fmt.Fprintf(r.w, "    %s\n", r.masker.Mask(line))
	}
}

// CommandFailed prints the failure line for a command.
func (r *PlainReporter) CommandFailed(label string, exitCode int) {
	fmt.Fprintf(r.w, "x %s failed (exit code %d)\n", label, exitCode)
}

// StepCompleted prints the step success line.
func (r *PlainReporter) StepCompleted(name string) {
	fmt.Fprintf(r.w, "ok %s\n", name)
}

// StepCancelled prints the cancellation line for a declined confirm gate.
func (r *PlainReporter) StepCancelled(name string) {
	fmt.Fprintf(r.w, "Release cancelled at the '%s' step.\n", name)
}

// RunCompleted prints the final outcome and, on failure after a step started,
// a resume hint.
func (r *PlainReporter) RunCompleted(success bool, message string) {
	if success {
		if message != "" {
			fmt.Fprintf(r.w, "Release complete. %s\n", message)
		} else {
			fmt.Fprintln(r.w, "Release complete.")
		}
		return
	}

	fmt.Fprintf(r.w, "Release failed. %s\n", message)
	if r.currentStep != "" {
		fmt.Fprintf(r.w, "Resume with: %s release --from %s\n", r.tool, r.currentStep)
	}
}
