package pipeline

// UIMode resolves how the release renders and interacts, from the flags and
// stream redirection. Rich chooses the rich renderer over the plain one;
// Interactive enables prompts and the per-step toggle picker. The two are
// independent: --no-ui on a terminal still prompts, and --ui piped to a file
// still renders richly (just without prompting).
type UIMode struct {
	// Rich means use the rich (TUI) reporter.
	Rich bool

	// Interactive means prompt the user (confirmation gates, plan toggles).
	Interactive bool
}

// ResolveUIMode computes the UI mode from the flags and stream redirection.
func ResolveUIMode(ui, noUI, yes, outputRedirected, inputRedirected bool) UIMode {
	return UIMode{
		Rich:        !noUI && (ui || !outputRedirected),
		Interactive: !yes && !outputRedirected && !inputRedirected,
	}
}

// Prompter asks the user to confirm a gated step. Abstracted so the engine
// stays headless and testable.
type Prompter interface {
	// Confirm returns true to proceed, false to stop the release at this step.
	Confirm(message string) bool
}

// FixedPrompter is a non-interactive prompter that always answers the same
// way: true for --yes, false when there is no TTY to ask (so a confirm gate
// safely stops rather than blocking or auto-proceeding).
type FixedPrompter struct {
	Answer bool
}

// Confirm answers with the fixed value.
func (p FixedPrompter) Confirm(string) bool { return p.Answer }

// PlanChooser lets the user review the plan and toggle which steps run before
// the release starts. Abstracted so an interactive picker is swappable for a
// passthrough in non-interactive runs (and tests).
type PlanChooser interface {
	// Choose returns the steps to run, possibly with some marked skipped by
	// the user.
	Choose(steps []ResolvedStep) []ResolvedStep
}

// PassthroughChooser runs every step as resolved; used when there is no
// terminal to interact with.
type PassthroughChooser struct{}

// Choose returns the steps unchanged.
func (PassthroughChooser) Choose(steps []ResolvedStep) []ResolvedStep { return steps }
