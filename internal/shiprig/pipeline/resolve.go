package pipeline

import (
	"fmt"
	"slices"
	"strings"
)

// DefaultTool is the default tool used to invoke changesets for built-in steps.
const DefaultTool = "changeset"

// DefaultCommitMessage is the default commit message for the built-in commit step.
const DefaultCommitMessage = "chore: release"

// DefaultOrder lists the steps run when order is not configured. `build` is
// early (before publish) so it doubles as a packaging preflight — a broken build
// fails the release before anything ships. The forge step (`githubRelease`)
// attaches the build's release assets after creating the release.
var DefaultOrder = []string{"version", "commit", "build", "publish", "push", "githubRelease"}

var (
	commandBuiltins = []string{"version", "commit", "publish", "push"}
	nativeBuiltins  = []string{"build", "githubRelease"}
)

// DefaultConfirmMessage is the default confirmation prompt when a step sets
// confirm: true with no message.
func DefaultConfirmMessage(step string) string {
	return fmt.Sprintf("Proceed with the '%s' step?", step)
}

// StepKind says how a step's action is carried out.
type StepKind int

const (
	// StepKindCommands runs a list of commands (built-in defaults or a custom run).
	StepKindCommands StepKind = iota

	// StepKindNative runs a handler provided by the host (e.g. per-package
	// forge releases).
	StepKindNative
)

// ResolvedStep is a step after its config and built-in defaults have been
// merged into a concrete, runnable form: the commands to run for the action,
// plus its before/after hooks. This is what the pipeline executes and what
// the reporter renders in the plan.
type ResolvedStep struct {
	// Name is the step name (built-in or custom).
	Name string

	// SkipReason says why the step will not run; "" when it will.
	SkipReason string

	// Before are commands to run before the action.
	Before []CommandSpec

	// Action is the step's own commands.
	Action []CommandSpec

	// After are commands to run after the action.
	After []CommandSpec

	// IsBuiltin is true when the step maps to a built-in (vs a purely custom
	// run step).
	IsBuiltin bool

	// Confirm, when non-nil, is the prompt shown to gate the step.
	Confirm *string

	// Kind says whether the action runs commands or a native handler.
	Kind StepKind
}

// Enabled reports whether the step will run.
func (s ResolvedStep) Enabled() bool { return s.SkipReason == "" }

// ResolveOptions filter the resolved plan.
type ResolveOptions struct {
	// Only, when non-empty, skips every step not named in it.
	Only []string

	// Skip skips the named steps.
	Skip []string

	// From skips the steps before the named one (for resuming after a failure).
	From string

	// To skips the steps after the named one.
	To string
}

// Resolve merges config and built-in defaults into the concrete ordered list
// of steps, applying the only/skip filters and the from/to range. It returns
// an error when a step has no built-in action and no run command, or when
// from/to name a step that is not in the order. Skipped steps stay in the
// plan with their reason.
func Resolve(config *Config, opts ResolveOptions) ([]ResolvedStep, error) {
	order := config.Order
	if order == nil {
		order = DefaultOrder
	}

	fromIndex, err := indexOfBound(order, opts.From, "from", 0)
	if err != nil {
		return nil, err
	}
	toIndex, err := indexOfBound(order, opts.To, "to", len(order)-1)
	if err != nil {
		return nil, err
	}

	resolved := make([]ResolvedStep, 0, len(order))

	for index, name := range order {
		stepConfig := config.Steps[name]

		commandAction, hasAction := stepAction(name, config, stepConfig)
		isNative := !hasAction && slices.Contains(nativeBuiltins, name)

		if !hasAction && !isNative {
			return nil, fmt.Errorf("release step '%s' is not a built-in and defines no 'run' command", name)
		}

		kind := StepKindCommands
		if isNative {
			kind = StepKindNative
		}

		var before, after []CommandSpec
		if stepConfig != nil {
			before = stepConfig.Before
			after = stepConfig.After
		}

		resolved = append(resolved, ResolvedStep{
			Name:       name,
			SkipReason: skipReasonFor(name, stepConfig, opts, index, fromIndex, toIndex),
			Before:     before,
			Action:     commandAction,
			After:      after,
			IsBuiltin:  slices.Contains(commandBuiltins, name) || slices.Contains(nativeBuiltins, name),
			Confirm:    confirmMessageFor(name, stepConfig),
			Kind:       kind,
		})
	}

	return resolved, nil
}

// stepAction returns the step's command action: an explicit run command
// overrides the built-in default. The second result is false when the step is
// neither (it may still be a native built-in).
func stepAction(name string, config *Config, stepConfig *StepConfig) ([]CommandSpec, bool) {
	if stepConfig != nil && stepConfig.Run != nil {
		return stepConfig.Run, true
	}

	tool := toolOf(config)
	var extraArgs []string
	if stepConfig != nil {
		extraArgs = stepConfig.Args
	}

	switch name {
	case "version":
		return []CommandSpec{ShellCommand(joinShell(tool, "version", extraArgs))}, true

	case "commit":
		message := DefaultCommitMessage
		if stepConfig != nil && stepConfig.Message != nil {
			message = *stepConfig.Message
		}
		// Commit only when `git add -A` actually staged something. The `version`
		// step (and changerig's `commit` config key) may have already committed,
		// leaving an empty index — a bare `git commit` then exits non-zero with
		// "nothing to commit" and fails the whole release. `git diff --cached
		// --quiet` exits 0 when the index is clean, short-circuiting the commit.
		return []CommandSpec{
			ArgvCommand("git", "add", "-A"),
			ShellCommand("git diff --cached --quiet || git commit -m " + shellSingleQuote(message)),
		}, true

	case "publish":
		return []CommandSpec{ShellCommand(joinShell(tool, "publish", extraArgs))}, true

	case "push":
		push := append([]string{"git", "push", "--follow-tags"}, extraArgs...)
		return []CommandSpec{ArgvCommand(push...)}, true

	default:
		return nil, false
	}
}

// shellSingleQuote wraps s in single quotes for safe interpolation into a
// /bin/sh command line. Each embedded single quote is escaped with the standard
// POSIX sequence: close the quote, emit a backslash-escaped quote, reopen it.
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func toolOf(config *Config) string {
	if config.Tool == "" {
		return DefaultTool
	}
	return config.Tool
}

func joinShell(tool, subcommand string, extraArgs []string) string {
	args := ""
	if len(extraArgs) > 0 {
		args = " " + strings.Join(extraArgs, " ")
	}
	return fmt.Sprintf("%s %s%s", tool, subcommand, args)
}

func confirmMessageFor(name string, stepConfig *StepConfig) *string {
	if stepConfig == nil || stepConfig.Confirm == nil || !stepConfig.Confirm.Enabled {
		return nil
	}
	if stepConfig.Confirm.Custom != nil {
		return stepConfig.Confirm.Custom
	}
	message := DefaultConfirmMessage(name)
	return &message
}

func indexOfBound(order []string, step, option string, fallback int) (int, error) {
	if step == "" {
		return fallback, nil
	}
	if index := slices.Index(order, step); index >= 0 {
		return index, nil
	}
	return 0, fmt.Errorf("--%s step '%s' is not in the release order", option, step)
}

func skipReasonFor(
	name string,
	stepConfig *StepConfig,
	opts ResolveOptions,
	index, fromIndex, toIndex int,
) string {
	if stepConfig != nil && stepConfig.Enabled != nil && !*stepConfig.Enabled {
		return "disabled"
	}
	if index < fromIndex {
		return "before --from"
	}
	if index > toIndex {
		return "after --to"
	}
	if len(opts.Only) > 0 && !slices.Contains(opts.Only, name) {
		return "not in --only"
	}
	if slices.Contains(opts.Skip, name) {
		return "--skip"
	}
	return ""
}
