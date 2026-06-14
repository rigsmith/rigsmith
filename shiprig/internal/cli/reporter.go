package cli

import (
	"fmt"
	"io"

	"github.com/charmbracelet/lipgloss"
	"github.com/rigsmith/core/brand"
	"github.com/rigsmith/shiprig/internal/pipeline"
)

// richReporter is the styled terminal reporter (the C# TuiReleaseReporter
// equivalent). It renders sequentially — never a live frame — so confirm
// gates can prompt mid-run. Everything it prints passes through the masker.
type richReporter struct {
	w      io.Writer
	masker *pipeline.SecretMasker
	tool   string

	lastStarted string
}

var (
	ruleStyle    = lipgloss.NewStyle().Foreground(brand.AccentShip).Bold(true)
	stepOkStyle  = lipgloss.NewStyle().Foreground(brand.Green)
	skipStyle    = lipgloss.NewStyle().Foreground(brand.Muted)
	failStyle    = lipgloss.NewStyle().Foreground(brand.Red).Bold(true)
	cancelStyle  = lipgloss.NewStyle().Foreground(brand.Amber).Bold(true)
	successPanel = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(brand.Green).Padding(0, 1)
	failPanel    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(brand.Red).Padding(0, 1)
	cancelPanel  = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(brand.Amber).Padding(0, 1)
)

func newRichReporter(w io.Writer, masker *pipeline.SecretMasker, tool string) *richReporter {
	if tool == "" {
		tool = pipeline.DefaultTool
	}
	return &richReporter{w: w, masker: masker, tool: tool}
}

// Plan renders the step table: name, action, gate, and whether it runs.
func (r *richReporter) Plan(steps []pipeline.ResolvedStep, dryRun bool) {
	title := "Release plan"
	if dryRun {
		title = "Release plan (dry run)"
	}
	fmt.Fprintln(r.w, ruleStyle.Render("── "+title+" ─────────────────────────"))
	for _, s := range steps {
		state := stepOkStyle.Render("run")
		if !s.Enabled() {
			state = skipStyle.Render("skip: " + s.SkipReason)
		}
		gate := ""
		if s.Confirm != nil {
			gate = "  " + skipStyle.Render("confirm: "+*s.Confirm)
		}
		fmt.Fprintf(r.w, "  %-14s %s%s\n", s.Name, state, gate)
		if s.Kind == pipeline.StepKindNative {
			fmt.Fprintf(r.w, "      %s\n", skipStyle.Render("(per-package forge release)"))
			continue
		}
		for _, c := range s.Action {
			fmt.Fprintf(r.w, "      %s\n", skipStyle.Render(r.masker.Mask(pipeline.DescribeCommand(c))))
		}
	}
	fmt.Fprintln(r.w)
}

func (r *richReporter) StepStarted(name string) {
	r.lastStarted = name
	fmt.Fprintln(r.w, ruleStyle.Render("── "+name+" "+"─────────────────────────"))
}

func (r *richReporter) StepSkipped(name, reason string) {
	fmt.Fprintf(r.w, "%s\n", skipStyle.Render("── "+name+" skipped ("+reason+")"))
}

func (r *richReporter) StepCompleted(name string) {
	fmt.Fprintf(r.w, "%s\n", stepOkStyle.Render("ok "+name))
}

func (r *richReporter) StepCancelled(name string) {
	fmt.Fprintln(r.w, cancelPanel.Render(cancelStyle.Render("Release stopped at the '"+name+"' confirm gate.")))
}

func (r *richReporter) CommandStarted(label string, command pipeline.CommandSpec) {
	fmt.Fprintf(r.w, "  $ %s\n", r.masker.Mask(pipeline.DescribeCommand(command)))
}

func (r *richReporter) CommandOutput(lines []string) {
	for _, l := range lines {
		fmt.Fprintf(r.w, "    %s\n", r.masker.Mask(l))
	}
}

func (r *richReporter) CommandFailed(label string, exitCode int) {
	fmt.Fprintln(r.w, failStyle.Render(fmt.Sprintf("x %s failed (exit code %d)", r.masker.Mask(label), exitCode)))
}

func (r *richReporter) RunCompleted(success bool, message string) {
	msg := r.masker.Mask(message)
	if success {
		body := "Release complete."
		if msg != "" {
			body = msg
		}
		fmt.Fprintln(r.w, successPanel.Render(stepOkStyle.Render(body)))
		return
	}
	body := "Release failed."
	if msg != "" {
		body = msg
	}
	if r.lastStarted != "" {
		body += "\n" + fmt.Sprintf("Resume with: %s release --from %s", r.tool, r.lastStarted)
	}
	fmt.Fprintln(r.w, failPanel.Render(failStyle.Render(body)))
}
