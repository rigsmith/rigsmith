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
// fails the release before anything ships. `tag` is its own step (promoted out
// of publish): publish narrows to the registry push, `tag` creates the local
// tags, and `push --follow-tags` puts them on the remote before `release`. The
// `release` step (forge) creates the release and attaches the build's assets.
// `issues` runs last: it comments on / closes the issues the release resolves
// (a no-op unless the `issues` config is enabled). `sign` sits right after
// `build`: it is a post-build pass that signs the produced artifacts (e.g. Azure
// Trusted Signing over .exe/.msi, or a codesign/rcodesign command over
// .dmg/.app) so `release` attaches the signed files — a no-op unless an
// ecosystem's `signing.signers` is configured.
var DefaultOrder = []string{"version", "commit", "build", "sign", "publish", "tag", "push", "release", "issues"}

var (
	commandBuiltins = []string{"version", "commit", "publish", "tag", "push"}
	// build, sign, the forge `release` step, and `issues` run host-registered handlers.
	nativeBuiltins = []string{"build", "sign", "release", "issues"}
)

// NativeStepDescription is the human label for a native step's action — it runs a
// host-registered handler rather than a shell command, so the reporters show this
// instead of a command line.
func NativeStepDescription(name string) string {
	switch name {
	case "build":
		return "build distributable artifacts"
	case "sign":
		return "sign built artifacts (post-build code signing)"
	case "release":
		return "per-package forge release"
	case "issues":
		return "comment on / close resolved issues"
	default:
		return "native step"
	}
}

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

	// StepKindScript runs the step's "script" (Tengo code) as its action.
	StepKindScript
)

// ResolvedStep is a step after its config and built-in defaults have been
// merged into a concrete, runnable form: the commands to run for the action,
// plus its before/after hooks. This is what the pipeline executes and what
// the reporter renders in the plan.
type ResolvedStep struct {
	// Name is the step id (built-in or custom): the key in Order and the target
	// of --only/--skip/--from/--to and the resume hint.
	Name string

	// DisplayName is the human label for plans and progress output, from the
	// step's "name" config; it falls back to Name when unset. Use Label() to
	// read it with the fallback applied.
	DisplayName string

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

	// OverridesNative is true when a custom `run` replaces a native step's
	// action (build/release/issues). The native handler is skipped; reporters
	// surface a note so the substitution is not silent.
	OverridesNative bool

	// Confirm, when non-nil, is the prompt shown to gate the step.
	Confirm *string

	// Kind says whether the action runs commands or a native handler.
	Kind StepKind

	// DryRunAction holds the commands to execute for this step during a
	// `--dry-run` (the action itself, or a configured alternate). nil means the
	// action is listed but not executed.
	DryRunAction []CommandSpec

	// DryRunHidden hides the action from the dry-run plan ("dryRun": false).
	DryRunHidden bool

	// If is the Tengo gate expression ("" when none); evaluated at run time —
	// a falsy result skips the step.
	If string

	// Script is the step's Tengo code, when Kind is StepKindScript.
	Script string
}

// Enabled reports whether the step will run.
func (s ResolvedStep) Enabled() bool { return s.SkipReason == "" }

// Label is the human-facing name for the step: DisplayName when set, else the
// step id. Reporters render this; the engine keys everything else off Name.
func (s ResolvedStep) Label() string {
	if s.DisplayName != "" {
		return s.DisplayName
	}
	return s.Name
}

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

	// DryBuild runs only the `build` step (so the release's artifacts are built
	// locally) and skips every other step — nothing is committed, tagged, pushed,
	// or published. The build step itself runs in snapshot mode (set by the host
	// handler). It is a real run, distinct from --dry-run's plan-only preview.
	DryBuild bool

	// Ecosystems is the set of ecosystem ids present in this release (the host
	// fills it from discovery). A step that declares `ecosystems` matching none
	// of these is skipped. nil disables ecosystem filtering entirely (every step
	// runs regardless of its `ecosystems`) — used by tests and minimal pipelines.
	Ecosystems []string

	// KnownEcosystems is the set of valid ecosystem ids (the host fills it from
	// the registry). When non-nil, a step listing an id outside this set is a
	// config error. nil skips that validation.
	KnownEcosystems []string
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

		if err := validateStepEcosystems(name, stepConfig, opts.KnownEcosystems); err != nil {
			return nil, err
		}

		// A "script" step's action is Tengo code, not commands. It cannot also
		// set "run".
		isScript := stepConfig != nil && stepConfig.Script != nil
		if isScript && stepConfig.Run != nil {
			return nil, fmt.Errorf("release step '%s' sets both 'run' and 'script'; use one", name)
		}

		var commandAction []CommandSpec
		var hasAction bool
		if !isScript {
			commandAction, hasAction = stepAction(name, config, stepConfig)
		}
		isNative := !isScript && !hasAction && slices.Contains(nativeBuiltins, name)

		if !isScript && !hasAction && !isNative {
			return nil, fmt.Errorf("release step '%s' is not a built-in and defines no 'run' or 'script'", name)
		}

		kind := StepKindCommands
		switch {
		case isScript:
			kind = StepKindScript
		case isNative:
			kind = StepKindNative
		}

		var before, after []CommandSpec
		var displayName, ifExpr, script string
		if stepConfig != nil {
			before = stepConfig.Before
			after = stepConfig.After
			if stepConfig.Name != nil {
				displayName = strings.TrimSpace(*stepConfig.Name)
			}
			if stepConfig.If != nil {
				ifExpr = strings.TrimSpace(*stepConfig.If)
			}
			if stepConfig.Script != nil {
				script = stepConfig.Script.Code
			}
		}

		// A native built-in with an explicit run or script is no longer native:
		// the custom action replaces the native handler. Flag it so reporters can
		// note the substitution.
		overridesNative := slices.Contains(nativeBuiltins, name) && stepConfig != nil &&
			(stepConfig.Run != nil || stepConfig.Script != nil)

		dryAction, dryHidden := dryRunPlan(stepConfig, commandAction)
		if dryAction == nil && !dryHidden {
			dryAction = defaultDryAction(name, config, stepConfig)
		}

		resolved = append(resolved, ResolvedStep{
			Name:            name,
			DisplayName:     displayName,
			SkipReason:      skipReasonFor(name, stepConfig, opts, index, fromIndex, toIndex),
			Before:          before,
			Action:          commandAction,
			After:           after,
			IsBuiltin:       slices.Contains(commandBuiltins, name) || slices.Contains(nativeBuiltins, name),
			OverridesNative: overridesNative,
			Confirm:         confirmMessageFor(name, stepConfig),
			Kind:            kind,
			DryRunAction:    dryAction,
			DryRunHidden:    dryHidden,
			If:              ifExpr,
			Script:          script,
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
		// Stage everything by default; when `paths` is set, scope the commit to
		// exactly those paths so unrelated WIP in the tree stays out of the
		// release commit.
		add := []string{"git", "add", "-A"}
		if stepConfig != nil && len(stepConfig.Paths) > 0 {
			add = append([]string{"git", "add", "--"}, stepConfig.Paths...)
		}
		// Commit only when staging actually produced something. The `version`
		// step (and changerig's `commit` config key) may have already committed,
		// leaving an empty index — a bare `git commit` then exits non-zero with
		// "nothing to commit" and fails the whole release. `git diff --cached
		// --quiet` exits 0 when the index is clean, short-circuiting the commit.
		return []CommandSpec{
			ArgvCommand(add...),
			ShellCommand("git diff --cached --quiet || git commit -m " + shellSingleQuote(message)),
		}, true

	case "publish":
		// Tagging is the `tag` step's job in the pipeline, so publish narrows to
		// the registry push (--no-git-tag). A non-default order that drops the
		// `tag` step can re-enable tagging via the publish step's args.
		return []CommandSpec{ShellCommand(joinShell(tool, "publish --no-git-tag", extraArgs))}, true

	case "tag":
		return []CommandSpec{ShellCommand(joinShell(tool, "tag", extraArgs))}, true

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

// dryRunPlan derives a step's dry-run behaviour from its config: the commands to
// execute during a dry run (nil = none) and whether to hide the action from the
// plan. action is the step's resolved action, used when "dryRun": true runs the
// action itself rather than an alternate.
func dryRunPlan(stepConfig *StepConfig, action []CommandSpec) (dryAction []CommandSpec, hidden bool) {
	if stepConfig == nil || stepConfig.DryRun == nil {
		return nil, false
	}
	spec := stepConfig.DryRun
	if spec.Hide {
		return nil, true
	}
	if !spec.Execute {
		return nil, false
	}
	if spec.Commands != nil {
		return spec.Commands, false
	}
	return action, false
}

// changelogTools are the CLIs whose `version` understands `--changelog` (the
// preview flag rigsmith added). An external backing tool — e.g. "npx changeset"
// for the Node @changesets/cli — does not, so it's excluded below.
var changelogTools = []string{"shiprig", "changerig", "changeset"}

// defaultDryAction supplies a built-in step's dry-run preview when the user
// configured none. Only `version` has a side-effect-free preview worth running:
// `<tool> version --changelog` renders the bump plan and the exact changelog
// notes the step would write, while writing nothing itself — so a dry-run
// release surfaces what will be versioned instead of a bare `version` plan line.
// A step whose action a custom `run`/`script` replaced isn't the built-in
// `version` anymore, so its flags can't be assumed; return nil there. Likewise
// for a backing tool that doesn't support `--changelog` — the preview would just
// fail the dry run, so fall back to the bare plan line.
func defaultDryAction(name string, config *Config, stepConfig *StepConfig) []CommandSpec {
	if name != "version" {
		return nil
	}
	if stepConfig != nil && (stepConfig.Run != nil || stepConfig.Script != nil) {
		return nil
	}
	tool := toolOf(config)
	if !slices.Contains(changelogTools, tool) {
		return nil
	}
	var extraArgs []string
	if stepConfig != nil {
		extraArgs = stepConfig.Args
	}
	return []CommandSpec{ShellCommand(joinShell(tool, "version --changelog", extraArgs))}
}

// validateStepEcosystems rejects a step that targets an ecosystem id outside the
// known set. known == nil skips validation (tests / minimal pipelines).
func validateStepEcosystems(name string, stepConfig *StepConfig, known []string) error {
	if known == nil || stepConfig == nil {
		return nil
	}
	for _, eco := range stepConfig.Ecosystems {
		if !slices.Contains(known, eco) {
			return fmt.Errorf("release step '%s' targets unknown ecosystem '%s' (known: %s)",
				name, eco, strings.Join(known, ", "))
		}
	}
	return nil
}

// ecosystemSkipReason returns a skip reason when a step targets ecosystems but
// the release includes none of them. present == nil disables filtering (returns
// ""); a step with no `ecosystems` always returns "".
func ecosystemSkipReason(stepConfig *StepConfig, present []string) string {
	if present == nil || stepConfig == nil || len(stepConfig.Ecosystems) == 0 {
		return ""
	}
	for _, want := range stepConfig.Ecosystems {
		if slices.Contains(present, want) {
			return ""
		}
	}
	return fmt.Sprintf("no %s packages in this release", strings.Join(stepConfig.Ecosystems, "/"))
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
	// DryBuild is authoritative: build always runs, everything else is skipped —
	// regardless of disabled/from/to/only/skip — so a dry-build can never become a
	// no-op or surface a non-dry-build skip reason.
	if opts.DryBuild {
		if name == "build" {
			return ""
		}
		return "dry-build: build only"
	}
	if stepConfig != nil && stepConfig.Enabled != nil && !*stepConfig.Enabled {
		return "disabled"
	}
	if reason := ecosystemSkipReason(stepConfig, opts.Ecosystems); reason != "" {
		return reason
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
