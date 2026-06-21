package pipeline

import (
	"fmt"
	"strings"

	"github.com/rigsmith/rigsmith/core/script"
	"github.com/rigsmith/rigsmith/core/shellrun"
)

// runScriptStep executes a step's "script" (Tengo code) as its action through
// the shared core/script runtime. The script gets the release `ctx` plus the
// side-effecting helpers (sh, cp/mv/rm/mkdir, log, fail) wired to this pipeline
// via pipelineScriptHost. In a dry run those side effects are previewed
// (reported, not executed) while the script's own logic still runs.
//
// The runtime's timeout bounds the Tengo VM's execution; it does NOT interrupt
// an external command started by sh() (the Runner seam takes no context), so a
// hung subprocess is not cancelled by the timeout.
func (p *Pipeline) runScriptStep(step ResolvedStep) bool {
	if err := script.Run(step.Script, p.scriptCtx, &pipelineScriptHost{p}); err != nil {
		p.reporter.CommandOutput([]string{err.Error()})
		p.reporter.CommandFailed(step.Name+" (script)", -1)
		return false
	}
	return true
}

// pipelineScriptHost adapts a Pipeline to script.Host, routing the script
// builtins' side effects through the pipeline's runner, reporter, work dir, and
// dry-run flag so they behave exactly like the rest of a release step.
type pipelineScriptHost struct{ p *Pipeline }

// Sh runs a shell command through the (portable) runner and returns its stdout.
// A non-zero exit aborts the script (the safe `set -e`-like default). In a dry
// run it is previewed, returning "".
func (h *pipelineScriptHost) Sh(cmd string) (string, error) {
	if h.p.scriptDryRun {
		h.p.reporter.CommandOutput([]string{"would run: " + cmd})
		return "", nil
	}
	h.p.reporter.CommandStarted("script", ShellCommand(cmd))
	output, code := dispatch(h.p.runner, ShellCommand(cmd), h.p.workDir)
	h.p.reporter.CommandOutput(output)
	if code != 0 {
		return "", fmt.Errorf("sh: command exited %d: %s", code, cmd)
	}
	return strings.Join(output, "\n"), nil
}

// FileOp performs a cp/mv/rm/mkdir in the step's working directory. In a dry
// run it is previewed (reported, not executed).
func (h *pipelineScriptHost) FileOp(name string, args []string) error {
	if h.p.scriptDryRun {
		h.p.reporter.CommandOutput([]string{"would " + name + " " + strings.Join(args, " ")})
		return nil
	}
	return shellrun.FileOp(name, h.p.workDir, args)
}

// Report writes a line to the release output (used by log()).
func (h *pipelineScriptHost) Report(line string) {
	h.p.reporter.CommandOutput([]string{line})
}
