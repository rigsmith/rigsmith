package pipeline

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
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
}

func newVariables(specs map[string]*VarSpec, runner Runner, masker *SecretMasker, workDir string) *variables {
	return &variables{
		specs:   specs,
		runner:  runner,
		masker:  masker,
		workDir: workDir,
		cache:   map[string]string{},
	}
}

// eagerNames lists the variables that opt out of lazy resolution and should
// be captured up front (sorted for a deterministic capture order).
func (v *variables) eagerNames() []string {
	var names []string
	for name, spec := range v.specs {
		if spec != nil && !spec.Lazy {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func (v *variables) resolve(name string) varResolution {
	if cached, ok := v.cache[name]; ok {
		return varSuccess(cached)
	}

	spec, ok := v.specs[name]
	if !ok || spec == nil || spec.Command == nil {
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
// the context map.
func interpolateCommand(context map[string]string, command CommandSpec) CommandSpec {
	if command.IsShell() {
		return ShellCommand(interpolate(context, command.Shell()))
	}
	argv := make([]string, len(command.Argv()))
	for i, token := range command.Argv() {
		argv[i] = interpolate(context, token)
	}
	return ArgvCommand(argv...)
}

// interpolate substitutes ${name} placeholders from the context map.
// ${env.NAME} reads a process environment variable (missing variables become
// empty). Unknown placeholders are left verbatim so values resolved later
// survive this pass untouched. The substitution is a single left-to-right
// pass — substituted values are never re-scanned.
func interpolate(context map[string]string, input string) string {
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
		if value, ok := resolveKey(context, key); ok {
			result.WriteString(value)
		} else {
			result.WriteString(input[start : end+1])
		}

		index = end + 1
	}

	return result.String()
}

func resolveKey(context map[string]string, key string) (string, bool) {
	if name, isEnv := strings.CutPrefix(key, "env."); isEnv {
		return os.Getenv(name), true
	}
	value, ok := context[key]
	return value, ok
}
