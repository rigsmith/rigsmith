package pipeline

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/rigsmith/rigsmith/core/envstack"
)

// varResolution is the outcome of resolving a variable: its value, or why the
// capture failed.
type varResolution struct {
	ok       bool
	value    string
	err      string
	exitCode int
}

func varSuccess(value string) varResolution {
	return varResolution{ok: true, value: value}
}

func varFailure(err string, exitCode int) varResolution {
	return varResolution{err: err, exitCode: exitCode}
}

// variables resolves vars by running their capture command and taking the
// trimmed output. Values are resolved at most once per run (cached) and only
// on demand, so a time-limited secret such as an OTP is fetched at the moment
// the step that needs it runs. Every resolved value is registered with the
// SecretMasker so it is redacted from all output.
type variables struct {
	specs   map[string]*VarSpec
	runner  Runner
	masker  *SecretMasker
	workDir string
	cache   map[string]string
	// scriptEval evaluates a "script" variable's Tengo expression to a string.
	scriptEval func(expr string) (string, error)
}

func newVariables(specs map[string]*VarSpec, runner Runner, masker *SecretMasker, workDir string, scriptEval func(string) (string, error)) *variables {
	return &variables{
		specs:      specs,
		runner:     runner,
		masker:     masker,
		workDir:    workDir,
		cache:      map[string]string{},
		scriptEval: scriptEval,
	}
}

// eagerNames lists the command-backed variables that opt out of lazy resolution
// and should be captured up front (sorted for a deterministic capture order).
// Literals need no capture, so they are not included.
func (v *variables) eagerNames() []string {
	var names []string
	for name, spec := range v.specs {
		if spec != nil && spec.Command != nil && !spec.Lazy {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// previewValue returns a variable's value when it can be known with no side
// effect — a literal, or a computed "script" var — for the dry-run preview. A
// captured (command) var returns false, so the preview placeholders it instead
// of running its command.
func (v *variables) previewValue(name string) (string, bool) {
	spec, ok := v.specs[name]
	if !ok || spec == nil {
		return "", false
	}
	if spec.Value != nil {
		return *spec.Value, true
	}
	if spec.Script != nil && v.scriptEval != nil {
		if val, err := v.scriptEval(*spec.Script); err == nil {
			return val, true
		}
	}
	return "", false
}

func (v *variables) resolve(name string) varResolution {
	if cached, ok := v.cache[name]; ok {
		return varSuccess(cached)
	}

	spec, ok := v.specs[name]
	if !ok || spec == nil {
		return varFailure(fmt.Sprintf("variable '%s' is not defined", name), -1)
	}

	// A literal resolves with no process and is not masked (it is config, not a
	// secret).
	if spec.Value != nil {
		v.cache[name] = *spec.Value
		return varSuccess(*spec.Value)
	}

	// A computed (script) var evaluates a Tengo expression; pure, so not masked.
	if spec.Script != nil {
		if v.scriptEval == nil {
			return varFailure(fmt.Sprintf("variable '%s': script evaluation unavailable", name), -1)
		}
		value, err := v.scriptEval(*spec.Script)
		if err != nil {
			return varFailure(fmt.Sprintf("variable '%s' script error: %v", name, err), -1)
		}
		v.cache[name] = value
		return varSuccess(value)
	}

	if spec.Command == nil {
		return varFailure(fmt.Sprintf("variable '%s' is not defined", name), -1)
	}

	output, exitCode := dispatch(v.runner, *spec.Command, v.workDir)
	if exitCode != 0 {
		return varFailure(fmt.Sprintf("capture command for variable '%s' failed", name), exitCode)
	}

	// Trimmed output is the value. Note: the runner merges stdout and stderr,
	// so a capture command should write only its value to stdout (true for
	// `op item get ... --otp` and similar).
	value := strings.TrimSpace(strings.Join(output, "\n"))

	v.masker.Add(value)
	v.cache[name] = value
	return varSuccess(value)
}

// varRefPattern finds the ${vars.NAME} references inside a command so they
// can be resolved before it runs.
var varRefPattern = regexp.MustCompile(`\$\{vars\.([^}]+)\}`)

// extractVarRefs returns the distinct variable names referenced by a command,
// in first-appearance order.
func extractVarRefs(command CommandSpec) []string {
	seen := map[string]bool{}
	var names []string

	collect := func(text string) {
		for _, match := range varRefPattern.FindAllStringSubmatch(text, -1) {
			if !seen[match[1]] {
				seen[match[1]] = true
				names = append(names, match[1])
			}
		}
	}

	if command.IsShell() {
		collect(command.Shell())
	} else {
		for _, token := range command.Argv() {
			collect(token)
		}
	}

	return names
}

// interpolateCommand substitutes ${name} placeholders in command text from
// the context map; ${env.NAME} resolves against env (see resolveKey).
func interpolateCommand(context, env map[string]string, command CommandSpec) CommandSpec {
	if command.IsShell() {
		return ShellCommand(interpolate(context, env, command.Shell()))
	}
	argv := make([]string, len(command.Argv()))
	for i, token := range command.Argv() {
		argv[i] = interpolate(context, env, token)
	}
	return ArgvCommand(argv...)
}

// interpolate substitutes ${name} placeholders from the context map.
// ${env.NAME} reads from env, the layered release environment (missing
// variables become empty). Unknown placeholders are left verbatim so values
// resolved later survive this pass untouched. The substitution is a single
// left-to-right pass — substituted values are never re-scanned.
func interpolate(context, env map[string]string, input string) string {
	if !strings.Contains(input, "${") {
		return input
	}

	var result strings.Builder
	result.Grow(len(input))
	index := 0

	for index < len(input) {
		start := strings.Index(input[index:], "${")
		if start < 0 {
			result.WriteString(input[index:])
			break
		}
		start += index

		end := strings.Index(input[start+2:], "}")
		if end < 0 {
			result.WriteString(input[index:])
			break
		}
		end += start + 2

		result.WriteString(input[index:start])

		key := input[start+2 : end]
		if value, ok := resolveKey(context, env, key); ok {
			result.WriteString(value)
		} else {
			result.WriteString(input[start : end+1])
		}

		index = end + 1
	}

	return result.String()
}

// resolveKey resolves a ${...} placeholder. ${env.NAME} reads from env, the
// layered release environment (.env/.env.local < ambient), falling back to the
// process environment when env is nil; a missing variable resolves to the empty
// string (still "found", so the placeholder is consumed). Other keys come from
// the context map.
func resolveKey(context, env map[string]string, key string) (string, bool) {
	if name, isEnv := strings.CutPrefix(key, "env."); isEnv {
		if env != nil {
			value, _ := envstack.Lookup(env, name) // missing → "", still consumed
			return value, true
		}
		return os.Getenv(name), true
	}
	value, ok := context[key]
	return value, ok
}
