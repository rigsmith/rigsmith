// Package pipeline is the headless release engine, ported from
// net-changesets' release orchestrator. It resolves the configured pipeline
// into concrete steps and runs them in order — each step's before hooks, then
// its action, then its after hooks — stopping on the first failure and
// invoking the global onError hook. All progress is reported through the
// Reporter interface; the engine itself draws nothing, so a plain renderer or
// a TUI can sit on top.
package pipeline

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/rigsmith/rigsmith/core/jsonc"
)

// CommandSpec is a single command to run as part of a release step or hook.
// It is either a shell string (run through the OS shell, so pipes/&&/
// redirection work) or an argv list (exec'd directly, one token per argument,
// no shell and no quoting hazards — the safe form for injected secrets).
type CommandSpec struct {
	shell   string
	argv    []string
	isShell bool
}

// ShellCommand returns a CommandSpec run through the OS shell.
func ShellCommand(shell string) CommandSpec {
	return CommandSpec{shell: shell, isShell: true}
}

// ArgvCommand returns a CommandSpec exec'd directly, one token per argument.
func ArgvCommand(argv ...string) CommandSpec {
	return CommandSpec{argv: argv}
}

// IsShell reports whether this is a shell command (vs a direct argv exec).
func (c CommandSpec) IsShell() bool { return c.isShell }

// Shell returns the shell command line ("" when this is an argv command).
func (c CommandSpec) Shell() string { return c.shell }

// Argv returns the argument vector (nil when this is a shell command).
func (c CommandSpec) Argv() []string { return c.argv }

// UnmarshalJSON reads one command: a string is a shell command; an array of
// strings is an argv command.
func (c *CommandSpec) UnmarshalJSON(data []byte) error {
	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	spec, err := commandFromValue(raw)
	if err != nil {
		return err
	}
	*c = spec
	return nil
}

func commandFromValue(value any) (CommandSpec, error) {
	switch v := value.(type) {
	case string:
		return ShellCommand(v), nil
	case []any:
		if len(v) == 0 {
			return CommandSpec{}, errors.New("an argv command must contain at least the executable")
		}
		argv := make([]string, len(v))
		for i, token := range v {
			s, ok := token.(string)
			if !ok {
				return CommandSpec{}, errors.New("an argv command must contain only strings")
			}
			argv[i] = s
		}
		return ArgvCommand(argv...), nil
	default:
		return CommandSpec{}, errors.New("a command must be a string (shell) or an array (argv)")
	}
}

// CommandList is a list of commands, accepting the ergonomic JSON shapes used
// in release.jsonc:
//
//   - "git push"                       — a single shell command (sugar for a one-element list)
//   - ["npm test", "git push"]         — a list of shell commands
//   - [["op", "item", "get", "--otp"]] — a list whose elements are argv arrays
//
// String elements become shell commands; array elements become argv commands,
// so the two can be mixed.
type CommandList []CommandSpec

// UnmarshalJSON implements the shapes documented on CommandList.
func (l *CommandList) UnmarshalJSON(data []byte) error {
	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	switch v := raw.(type) {
	case string:
		*l = CommandList{ShellCommand(v)}
		return nil
	case []any:
		list := make(CommandList, 0, len(v))
		for _, item := range v {
			spec, err := commandFromValue(item)
			if err != nil {
				return err
			}
			list = append(list, spec)
		}
		*l = list
		return nil
	default:
		return errors.New("a command list must be a string or an array of commands")
	}
}

// DryRunSpec controls a step's action during `--dry-run`, read from the step's
// "dryRun" key:
//
//   - absent         → the action is listed in the plan but not executed (default).
//   - true           → the action's own commands execute during the dry run.
//   - false          → the action is hidden from the plan and not executed.
//   - a command/list → those commands run (instead of the action) during the dry run.
//
// The plan always previews commands with their variables interpolated; this only
// decides what actually runs.
type DryRunSpec struct {
	// Hide is true for "dryRun": false — omit the action from the plan entirely.
	Hide bool
	// Execute is true when something should run during a dry run (the action, or
	// Commands when set).
	Execute bool
	// Commands is the alternate to run during a dry run; nil means "run the
	// action's own commands".
	Commands CommandList
}

// UnmarshalJSON reads a step's dryRun value (bool, or a command / command list).
func (d *DryRunSpec) UnmarshalJSON(data []byte) error {
	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if b, ok := raw.(bool); ok {
		*d = DryRunSpec{Hide: !b, Execute: b}
		return nil
	}
	var list CommandList
	if err := list.UnmarshalJSON(data); err != nil {
		return err
	}
	*d = DryRunSpec{Execute: true, Commands: list}
	return nil
}

// ConfirmValue is a step's confirmation gate, read from the step's "confirm"
// key: true enables a default prompt, a string sets a custom prompt, and
// false means no gate.
type ConfirmValue struct {
	// Enabled is false when the config explicitly set "confirm": false.
	Enabled bool
	// Custom is the custom prompt text, or nil for the default prompt.
	Custom *string
}

// ConfirmDefault returns an enabled gate with the default prompt.
func ConfirmDefault() *ConfirmValue { return &ConfirmValue{Enabled: true} }

// ConfirmText returns an enabled gate with a custom prompt.
func ConfirmText(message string) *ConfirmValue {
	return &ConfirmValue{Enabled: true, Custom: &message}
}

// UnmarshalJSON reads a step's confirm value (bool or string).
func (c *ConfirmValue) UnmarshalJSON(data []byte) error {
	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	switch v := raw.(type) {
	case bool:
		*c = ConfirmValue{Enabled: v}
		return nil
	case string:
		message := v
		*c = ConfirmValue{Enabled: true, Custom: &message}
		return nil
	default:
		return errors.New("'confirm' must be a boolean or a string message")
	}
}

// Config is the release-process configuration, read from
// .changeset/release.jsonc. Everything here is optional: with no file present
// the pipeline runs the built-in steps with defaults.
type Config struct {
	// Tool is the base command used to invoke changesets for the built-in
	// version/publish steps (defaults to "changeset").
	Tool string `json:"tool"`

	// Shell selects how shell-string commands run: "portable" (default — an
	// in-process, mostly-bash-compatible shell, identical on every OS) or
	// "system" (the OS shell, /bin/sh or cmd.exe, for exact OS-shell semantics or
	// a construct the portable shell doesn't support). Empty means "portable".
	// argv commands are unaffected (always exec'd directly). See ShellMode.
	Shell string `json:"shell"`

	// Order is the ordered list of step names to run. When nil, DefaultOrder
	// is used. Names may be built-ins or custom steps defined in Steps.
	Order []string `json:"order"`

	// Steps is the per-step configuration, keyed by step name.
	Steps map[string]*StepConfig `json:"steps"`

	// Hooks are the global hooks that wrap the whole run.
	Hooks *Hooks `json:"hooks"`

	// Vars are named variables captured from command output and injected into
	// step args via ${vars.name}.
	Vars map[string]*VarSpec `json:"vars"`
}

// StepConfig configures a single step in the pipeline.
type StepConfig struct {
	// Enabled controls whether the step runs; nil means "use the default"
	// (enabled).
	Enabled *bool `json:"enabled"`

	// Name is the human display label shown in plans and progress output. The
	// step's identity (the key in Order and the target of --only/--skip/--from)
	// stays the map key; this only changes how it is rendered. Empty falls back
	// to the step id.
	Name *string `json:"name"`

	// Before are commands run before the step's own action.
	Before CommandList `json:"before"`

	// After are commands run after the step's own action.
	After CommandList `json:"after"`

	// Run is the step's action. For a built-in step this overrides the
	// default action; for a custom step this is the action.
	Run CommandList `json:"run"`

	// DryRun controls the action during `--dry-run`: execute it, hide it, or run
	// an alternate (e.g. a tool's own --dry-run mode). nil lists it without
	// executing. See DryRunSpec.
	DryRun *DryRunSpec `json:"dryRun"`

	// Ecosystems restricts the step to releases that include at least one
	// package in one of the listed ecosystems ("node", "dotnet", "go", "rust").
	// Empty/absent runs for every release. A step that matches none of the
	// release's ecosystems is skipped (not an error), so one polyglot config
	// stays portable across single-ecosystem releases.
	Ecosystems []string `json:"ecosystems"`

	// Args are extra arguments appended to a built-in command
	// (e.g. ["--otp", "${vars.npmOtp}"]).
	Args []string `json:"args"`

	// Message is the commit message template, for the built-in commit step.
	Message *string `json:"message"`

	// Confirm pauses and asks the user to proceed before this step's action
	// runs. Bypassed by --yes.
	Confirm *ConfirmValue `json:"confirm"`

	// Forge, for the release step: "auto" (detect the forge from origin —
	// github.com/gitlab.com), an explicit "github" | "gitlab" | "gitea", or
	// "none" (tags only). Defaults to "auto".
	Forge string `json:"forge"`

	// ForgeURL is the self-hosted forge base URL, for an explicit gitlab/gitea on
	// a non-SaaS host (which can't be sniffed from origin). Empty for the SaaS
	// hosts. e.g. "https://git.example.com".
	ForgeURL string `json:"forgeURL"`
}

// Hooks are global hooks that bracket the entire release run.
type Hooks struct {
	// Before are commands run once before any step.
	Before CommandList `json:"before"`

	// After are commands run once after all steps succeed.
	After CommandList `json:"after"`

	// OnError are commands run when any step fails, before the run aborts.
	OnError CommandList `json:"onError"`
}

// VarSpec is a user-defined variable referenced as ${vars.NAME}. It is one of:
//
//   - a literal value — `{ "value": "x" }`, or the shorthand `"x"` (a bare
//     string). Literals resolve with no process and are NOT masked, so they suit
//     reusable config like a base path or a channel name.
//   - a captured value — `{ "command": … }`, whose trimmed stdout becomes the
//     value. Captured values ARE masked in all output (secret-safe), and `lazy`
//     defers the capture until first reference so a time-limited secret (an OTP)
//     stays fresh.
//
// Exactly one of Value / Command must be set (enforced by LoadConfig).
type VarSpec struct {
	Value   *string      `json:"value"`
	Command *CommandSpec `json:"command"`
	Lazy    bool         `json:"lazy"`
}

// UnmarshalJSON accepts the bare-string shorthand ("x" ⇒ a literal value) as
// well as the object form ({ "value": … } or { "command": …, "lazy": … }).
func (s *VarSpec) UnmarshalJSON(data []byte) error {
	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if str, ok := raw.(string); ok {
		*s = VarSpec{Value: &str}
		return nil
	}
	// Object form — alias to avoid recursing into this method.
	type varSpecObject VarSpec
	var obj varSpecObject
	if err := json.Unmarshal(data, &obj); err != nil {
		return err
	}
	*s = VarSpec(obj)
	return nil
}

// LoadConfig reads the release config at path. A missing file yields an
// empty (all-defaults) config, so the command works with zero configuration;
// a file that fails to parse is a real error.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, err
	}

	config := &Config{}
	if err := jsonc.Unmarshal(data, config); err != nil {
		return nil, fmt.Errorf("could not parse release config '%s': %w", path, err)
	}
	if err := validateVars(config.Vars); err != nil {
		return nil, fmt.Errorf("release config '%s': %w", path, err)
	}
	if _, err := ShellMode(config.Shell); err != nil {
		return nil, fmt.Errorf("release config '%s': %w", path, err)
	}
	return config, nil
}

// validateVars enforces that every variable sets exactly one of value/command.
func validateVars(vars map[string]*VarSpec) error {
	for name, spec := range vars {
		if spec == nil || (spec.Value == nil && spec.Command == nil) {
			return fmt.Errorf("variable '%s' must set either 'value' or 'command'", name)
		}
		if spec.Value != nil && spec.Command != nil {
			return fmt.Errorf("variable '%s' sets both 'value' and 'command'; use one", name)
		}
	}
	return nil
}

// ShellPortable and ShellSystem are the two values for Config.Shell.
const (
	ShellPortable = "portable"
	ShellSystem   = "system"
)

// ShellMode normalises the configured shell mode: "" defaults to "portable".
// Any value other than "portable"/"system" is a config error.
func ShellMode(shell string) (string, error) {
	switch shell {
	case "", ShellPortable:
		return ShellPortable, nil
	case ShellSystem:
		return ShellSystem, nil
	default:
		return "", fmt.Errorf("invalid 'shell' %q; want %q or %q", shell, ShellPortable, ShellSystem)
	}
}
