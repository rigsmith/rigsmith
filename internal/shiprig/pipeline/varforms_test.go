package pipeline

import (
	"errors"
	"strings"
	"testing"
)

// fail() is in scope for a computed var: a value that cannot be computed
// says why, and the run fails on that message rather than on an unresolved
// reference.
func TestComputedVarFailNamesTheReason(t *testing.T) {
	expr := `ctx.env.AVALLOY_BUILD ? "0.12.2-avalloy." + ctx.env.AVALLOY_BUILD : fail("set AVALLOY_BUILD")`
	config := &Config{
		Order: []string{"pack"},
		Vars:  map[string]*VarSpec{"ver": {Script: &expr}},
		Steps: map[string]*StepConfig{"pack": {Run: CommandList{ShellCommand("pack -p:Version=${vars.ver}")}}},
	}

	unset := New((&recordingRunner{}).run, &recordingReporter{}, NewSecretMasker(), &stubPrompter{answer: true}, "/tmp/repo", map[string]string{}, nil, nil)
	if unset.Run(mustResolve(t, config, ResolveOptions{}), config, true) {
		t.Fatal("a computed var that fails should fail the dry run")
	}
	if err := unset.vars.evalScriptVars(); err == nil || !strings.Contains(err.Error(), "set AVALLOY_BUILD") {
		t.Fatalf("err = %v, want the fail() message", err)
	}

	runner := &recordingRunner{}
	set := New(runner.run, &recordingReporter{}, NewSecretMasker(), &stubPrompter{answer: true}, "/tmp/repo", map[string]string{"AVALLOY_BUILD": "12"}, nil, nil)
	if !set.Run(mustResolve(t, config, ResolveOptions{}), config, false) {
		t.Fatal("run should succeed once the env var is set")
	}
	if got := strings.Join(runner.lines(), "\n"); !strings.Contains(got, "pack -p:Version=0.12.2-avalloy.12") {
		t.Errorf("computed var not interpolated: %q", got)
	}
}

// ${env.NAME} inside a literal var expands from the release environment, in
// the run and in the dry-run preview — it does not pass through to the shell.
func TestLiteralVarExpandsEnv(t *testing.T) {
	build := "${env.AVALLOY_BUILD}"
	config := &Config{
		Order: []string{"pack"},
		Vars:  map[string]*VarSpec{"build": {Value: &build}},
		Steps: map[string]*StepConfig{"pack": {Run: CommandList{ShellCommand("echo build=${vars.build}")}}},
	}
	env := map[string]string{"AVALLOY_BUILD": "7"}

	runner := &recordingRunner{}
	p := New(runner.run, &recordingReporter{}, NewSecretMasker(), &stubPrompter{answer: true}, "/tmp/repo", env, nil, nil)
	if !p.Run(mustResolve(t, config, ResolveOptions{}), config, false) {
		t.Fatal("run should succeed")
	}
	if got := strings.Join(runner.lines(), "\n"); !strings.Contains(got, "echo build=7") {
		t.Errorf("literal var did not expand ${env}: %q", got)
	}

	reporter := &recordingReporter{}
	dry := New((&recordingRunner{}).run, reporter, NewSecretMasker(), &stubPrompter{answer: true}, "/tmp/repo", env, nil, nil)
	if !dry.Run(mustResolve(t, config, ResolveOptions{}), config, true) {
		t.Fatal("dry run should succeed")
	}
	if line := DescribeCommand(plannedStep(t, reporter, "pack").Action[0]); !strings.Contains(line, "echo build=7") {
		t.Errorf("dry-run preview should show the expanded literal: %q", line)
	}
}

// A captured var picks its command by the OS the release runs on, falling back
// to `command`; an OS with neither is a clear failure.
func TestCapturedVarPicksCommandByOS(t *testing.T) {
	restore := currentOSToken
	t.Cleanup(func() { currentOSToken = restore })

	spec := &VarSpec{
		OS:      map[string]CommandSpec{"macos": ShellCommand("security find-generic-password -w"), "linux": ShellCommand("secret-tool lookup service feedz")},
		Command: ptr(ShellCommand("echo fallback")),
		Lazy:    true,
	}
	config := &Config{
		Order: []string{"push"},
		Vars:  map[string]*VarSpec{"key": spec},
		Steps: map[string]*StepConfig{"push": {Run: CommandList{ShellCommand("nuget push --api-key ${vars.key}")}}},
	}
	for osName, want := range map[string]string{"macos": "security find-generic-password -w", "linux": "secret-tool lookup service feedz", "windows": "echo fallback"} {
		currentOSToken = func() string { return osName }
		runner := &recordingRunner{responder: func(recordedCommand) ([]string, int) { return []string{"s3cret-" + osName}, 0 }}
		masker := NewSecretMasker()
		p := New(runner.run, &recordingReporter{}, masker, &stubPrompter{answer: true}, "/tmp/repo", nil, nil, nil)
		if !p.Run(mustResolve(t, config, ResolveOptions{}), config, false) {
			t.Fatalf("%s: run should succeed", osName)
		}
		got := strings.Join(runner.lines(), "\n")
		if !strings.Contains(got, want) {
			t.Errorf("%s: captured with %q, want %q", osName, got, want)
		}
		if !strings.Contains(got, "--api-key s3cret-"+osName) {
			t.Errorf("%s: value not interpolated: %q", osName, got)
		}
		if masked := masker.Mask("key is s3cret-" + osName); strings.Contains(masked, "s3cret") {
			t.Errorf("%s: captured value not masked: %q", osName, masked)
		}
	}

	// No fallback, no entry for this OS: the failure names both.
	currentOSToken = func() string { return "windows" }
	only := &VarSpec{OS: map[string]CommandSpec{"macos": ShellCommand("security")}}
	v := newVariables(map[string]*VarSpec{"key": only}, (&recordingRunner{}).run, NewSecretMasker(), "/tmp", nil, nil)
	if res := v.resolve("key"); res.ok || !strings.Contains(res.err, "no command for windows") || !strings.Contains(res.err, "macos") {
		t.Errorf("resolution = %+v, want a failure naming the OS and the keys", res)
	}
}

// A secret var goes through the credential resolver (op://, env:, cmd:) and
// is masked like a captured value; `lazy` keeps it unread until referenced.
func TestSecretVarResolvesAndMasks(t *testing.T) {
	ref := "env:FEEDZ_API_KEY"
	config := &Config{
		Order: []string{"push", "later"},
		Vars:  map[string]*VarSpec{"key": {Secret: &ref, Lazy: true}},
		Steps: map[string]*StepConfig{
			"push":  {Run: CommandList{ArgvCommand("nuget", "push", "--api-key", "${vars.key}")}},
			"later": {Run: CommandList{ShellCommand("echo done")}},
		},
	}
	runner := &recordingRunner{}
	masker := NewSecretMasker()
	p := New(runner.run, &recordingReporter{}, masker, &stubPrompter{answer: true}, "/tmp/repo", nil, nil, nil)
	steps := mustResolve(t, config, ResolveOptions{})
	asked := 0
	p.secretResolver = func(got string, _ *SecretMasker) (string, error) {
		asked++
		if got != ref {
			t.Errorf("secret ref = %q, want %q", got, ref)
		}
		return "feedz-token", nil
	}
	if !p.Run(steps, config, false) {
		t.Fatal("run should succeed")
	}
	if asked != 1 {
		t.Errorf("secret resolved %d times, want once", asked)
	}
	if got := strings.Join(runner.lines(), "\n"); !strings.Contains(got, "--api-key feedz-token") {
		t.Errorf("secret not interpolated: %q", got)
	}
	if masked := masker.Mask("token feedz-token here"); strings.Contains(masked, "feedz-token") {
		t.Errorf("secret not masked: %q", masked)
	}

	// A resolver error fails the step with the reason.
	p.vars.cache = map[string]string{}
	p.vars.secrets = func(string) (string, error) { return "", errors.New("op: not signed in") }
	if res := p.vars.resolve("key"); res.ok || !strings.Contains(res.err, "not signed in") {
		t.Errorf("resolution = %+v, want the resolver's error", res)
	}
}

// The default secret resolver reads the release, not the parent process:
// env: finds a key that lives only in .env.local, cmd: runs through the
// pipeline's runner (the release environment and repository it carries), and
// a blank result is a failure.
func TestSecretResolverReadsTheRelease(t *testing.T) {
	runner := &recordingRunner{responder: func(c recordedCommand) ([]string, int) {
		if !c.shell || !strings.Contains(c.args[0], "op item get feedz") {
			t.Errorf("cmd: ran %v, want the ref's command through the runner", c.args)
		}
		return []string{"from-cmd"}, 0
	}}
	env := map[string]string{"FEEDZ_API_KEY": "from-dotenv-local", "BLANK": "  "}
	envRef, cmdRef, blankRef := "env:FEEDZ_API_KEY", "cmd:op item get feedz --field credential", "env:BLANK"
	v := newVariables(map[string]*VarSpec{
		"key": {Secret: &envRef}, "cmd": {Secret: &cmdRef}, "blank": {Secret: &blankRef},
	}, runner.run, NewSecretMasker(), "/tmp/repo", env, nil)
	if res := v.resolve("key"); !res.ok || res.value != "from-dotenv-local" {
		t.Errorf("env: resolution = %+v, want the release environment's value", res)
	}
	if res := v.resolve("cmd"); !res.ok || res.value != "from-cmd" {
		t.Errorf("cmd: resolution = %+v, want the runner's output", res)
	}
	if res := v.resolve("blank"); res.ok || !strings.Contains(res.err, "BLANK is not set") {
		t.Errorf("blank env: resolution = %+v, want a failure naming the variable", res)
	}
}

// A lazy var that cannot be resolved fails the step with its reason on the
// output, not just an exit code.
func TestLazyVarFailureIsReported(t *testing.T) {
	restore := currentOSToken
	t.Cleanup(func() { currentOSToken = restore })
	currentOSToken = func() string { return "windows" }
	config := &Config{
		Order: []string{"push"},
		Vars:  map[string]*VarSpec{"key": {OS: map[string]CommandSpec{"macos": ShellCommand("security")}, Lazy: true}},
		Steps: map[string]*StepConfig{"push": {Run: CommandList{ShellCommand("push ${vars.key}")}}},
	}
	reporter := &outputReporter{}
	p := New((&recordingRunner{}).run, reporter, NewSecretMasker(), &stubPrompter{answer: true}, "/tmp/repo", nil, nil, nil)
	if p.Run(mustResolve(t, config, ResolveOptions{}), config, false) {
		t.Fatal("run should fail")
	}
	if got := strings.Join(reporter.output, "\n"); !strings.Contains(got, "no command for windows") {
		t.Errorf("output does not carry the reason: %q", got)
	}
}

// outputReporter records CommandOutput lines on top of the recording double.
type outputReporter struct {
	recordingReporter
	output []string
}

func (r *outputReporter) CommandOutput(lines []string) { r.output = append(r.output, lines...) }

// The config accepts each form once and refuses a mix or an unknown OS key.
func TestVarFormsValidation(t *testing.T) {
	for _, good := range []string{
		`{ "vars": { "k": { "secret": "op://vault/item/field", "lazy": true } } }`,
		`{ "vars": { "k": { "os": { "macos": "a", "linux": "b" } } } }`,
		`{ "vars": { "k": { "os": { "windows": ["cmd", "/c", "a"] }, "command": "b" } } }`,
	} {
		if _, err := parseConfig(t, good); err != nil {
			t.Errorf("%s: %v", good, err)
		}
	}
	for _, bad := range []string{
		`{ "vars": { "k": { "secret": "env:X", "command": "a" } } }`,
		`{ "vars": { "k": { "os": { "plan9": "a" } } } }`,
		`{ "vars": { "k": { "value": "a", "secret": "env:X" } } }`,
		`{ "vars": { "k": { "secret": "env:X", "os": {} } } }`,
		`{ "vars": { "k": { "os": {} } } }`,
		`{ "vars": { "k": { "secret": "  " } } }`,
	} {
		if _, err := parseConfig(t, bad); err == nil {
			t.Errorf("%s: accepted", bad)
		}
	}
}

// ${env.NAME} inside a captured var's command expands from the release
// environment before the command runs — in an `os` argv entry, token by token,
// so `security` is asked for the user, not for the literal placeholder.
func TestCapturedVarExpandsEnvInArgv(t *testing.T) {
	restore := currentOSToken
	t.Cleanup(func() { currentOSToken = restore })
	currentOSToken = func() string { return "macos" }

	config := &Config{
		Order: []string{"push"},
		Vars: map[string]*VarSpec{"key": {
			OS:   map[string]CommandSpec{"macos": ArgvCommand("security", "find-generic-password", "-a", "${env.USER}", "-s", "feedz-push", "-w")},
			Lazy: true,
		}},
		Steps: map[string]*StepConfig{"push": {Run: CommandList{ShellCommand("nuget push --api-key ${vars.key}")}}},
	}
	runner := &recordingRunner{responder: func(c recordedCommand) ([]string, int) {
		if c.hasArg("jcamp") {
			return []string{"s3cret"}, 0
		}
		if !c.shell {
			return []string{"could not find user"}, 44
		}
		return nil, 0
	}}
	p := New(runner.run, &recordingReporter{}, NewSecretMasker(), &stubPrompter{answer: true}, "/tmp/repo", map[string]string{"USER": "jcamp"}, nil, nil)
	if !p.Run(mustResolve(t, config, ResolveOptions{}), config, false) {
		t.Fatal("run should succeed once ${env.USER} expands in the capture command")
	}
	if len(runner.calls) < 1 || runner.calls[0].shell {
		t.Fatalf("capture should run as argv first, got %v", runner.lines())
	}
	if got := runner.calls[0].args; !runner.calls[0].hasArg("jcamp") || runner.calls[0].hasArg("${env.USER}") {
		t.Errorf("argv capture did not expand ${env.USER}: %q", got)
	}
	if got := strings.Join(runner.lines(), "\n"); !strings.Contains(got, "--api-key s3cret") {
		t.Errorf("captured value not interpolated: %q", got)
	}
}

// The same expansion applies to a `command` given as a shell string.
func TestCapturedVarExpandsEnvInShellCommand(t *testing.T) {
	config := &Config{
		Order: []string{"push"},
		Vars:  map[string]*VarSpec{"key": {Command: ptr(ShellCommand("security find-generic-password -a ${env.FEEDZ_ACCOUNT} -w"))}},
		Steps: map[string]*StepConfig{"push": {Run: CommandList{ShellCommand("nuget push --api-key ${vars.key}")}}},
	}
	runner := &recordingRunner{responder: func(recordedCommand) ([]string, int) { return []string{"s3cret"}, 0 }}
	p := New(runner.run, &recordingReporter{}, NewSecretMasker(), &stubPrompter{answer: true}, "/tmp/repo", map[string]string{"FEEDZ_ACCOUNT": "release-bot"}, nil, nil)
	if !p.Run(mustResolve(t, config, ResolveOptions{}), config, false) {
		t.Fatal("run should succeed")
	}
	got := strings.Join(runner.lines(), "\n")
	if !strings.Contains(got, "sh -c security find-generic-password -a release-bot -w") {
		t.Errorf("shell capture did not expand ${env}: %q", got)
	}
	if strings.Contains(got, "${env.") {
		t.Errorf("a placeholder reached the shell: %q", got)
	}
}

// A captured var whose command names an env var the release does not set
// fails the run before any hook or step runs — lazy or not — with a message
// naming the variable and the env var, and without the onError hook firing:
// nothing started, so there is nothing to clean up. The dry run refuses the
// same way. Set, even to an empty string, it passes; and with no layered
// environment the process environment is what counts.
func TestCapturedVarUnsetEnvFailsUpFront(t *testing.T) {
	restore := currentOSToken
	t.Cleanup(func() { currentOSToken = restore })
	currentOSToken = func() string { return "linux" }

	config := &Config{
		Order: []string{"build", "push"},
		Vars: map[string]*VarSpec{"key": {
			OS:   map[string]CommandSpec{"linux": ArgvCommand("secret-tool", "lookup", "account", "${env.NOPE}")},
			Lazy: true,
		}},
		Hooks: &Hooks{Before: CommandList{ShellCommand("echo before")}, OnError: CommandList{ShellCommand("echo cleanup")}},
		Steps: map[string]*StepConfig{
			"build": {Run: CommandList{ShellCommand("dotnet build")}},
			"push":  {Run: CommandList{ShellCommand("nuget push --api-key ${vars.key}")}},
		},
	}
	unset := map[string]string{"OTHER": "x"}

	runner := &recordingRunner{}
	reporter := &recordingReporter{}
	p := New(runner.run, reporter, NewSecretMasker(), &stubPrompter{answer: true}, "/tmp/repo", unset, nil, nil)
	if p.Run(mustResolve(t, config, ResolveOptions{}), config, false) {
		t.Fatal("run should fail on an unset ${env.NOPE} in a lazy captured var")
	}
	if len(runner.calls) != 0 {
		t.Errorf("nothing should run before the check — no step, no before hook, no onError hook — but ran %v", runner.lines())
	}
	for _, want := range []string{"variable 'key'", "${env.NOPE}", "not set in the release environment"} {
		if !strings.Contains(reporter.message, want) {
			t.Errorf("failure message %q should contain %q", reporter.message, want)
		}
	}

	dryRunner := &recordingRunner{}
	dryReporter := &recordingReporter{}
	dry := New(dryRunner.run, dryReporter, NewSecretMasker(), &stubPrompter{answer: true}, "/tmp/repo", unset, nil, nil)
	if dry.Run(mustResolve(t, config, ResolveOptions{}), config, true) {
		t.Fatal("dry run should fail on the same unset env var")
	}
	if len(dryRunner.calls) != 0 {
		t.Errorf("dry run should run nothing, but ran %v", dryRunner.lines())
	}
	if !strings.HasPrefix(dryReporter.message, "dry run: ") || !strings.Contains(dryReporter.message, "variable 'key'") || !strings.Contains(dryReporter.message, "${env.NOPE}") {
		t.Errorf("dry-run message = %q, want the check's message with the dry-run prefix", dryReporter.message)
	}

	// Set — even empty — passes the check; the value then expands as usual.
	for _, value := range []string{"acct", ""} {
		setRunner := &recordingRunner{responder: func(recordedCommand) ([]string, int) { return []string{"s3cret"}, 0 }}
		set := New(setRunner.run, &recordingReporter{}, NewSecretMasker(), &stubPrompter{answer: true}, "/tmp/repo", map[string]string{"NOPE": value}, nil, nil)
		if !set.Run(mustResolve(t, config, ResolveOptions{}), config, false) {
			t.Fatalf("NOPE=%q: run should succeed once the env var is set", value)
		}
		if got := strings.Join(setRunner.lines(), "\n"); !strings.Contains(got, "secret-tool lookup account "+value) || strings.Contains(got, "${env.NOPE}") {
			t.Errorf("NOPE=%q: capture command should carry the value: %q", value, got)
		}
	}

	// No layered environment: the process environment is read instead.
	t.Setenv("NOPE", "from-process")
	nilEnv := New((&recordingRunner{responder: func(recordedCommand) ([]string, int) { return []string{"s3cret"}, 0 }}).run, &recordingReporter{}, NewSecretMasker(), &stubPrompter{answer: true}, "/tmp/repo", nil, nil, nil)
	if !nilEnv.Run(mustResolve(t, config, ResolveOptions{}), config, false) {
		t.Fatal("with a nil env the process environment should satisfy the check")
	}

	// A literal's unset ${env.NAME} still expands to "" rather than failing.
	literal := "${env.NOPE_LITERAL}"
	literalConfig := &Config{
		Order: []string{"pack"},
		Vars:  map[string]*VarSpec{"build": {Value: &literal}},
		Steps: map[string]*StepConfig{"pack": {Run: CommandList{ShellCommand("echo build=${vars.build}")}}},
	}
	literalRunner := &recordingRunner{}
	lp := New(literalRunner.run, &recordingReporter{}, NewSecretMasker(), &stubPrompter{answer: true}, "/tmp/repo", unset, nil, nil)
	if !lp.Run(mustResolve(t, literalConfig, ResolveOptions{}), literalConfig, false) {
		t.Fatal("a literal naming an unset env var is not an error")
	}
	if got := strings.Join(literalRunner.lines(), "\n"); got != "sh -c echo build=" {
		t.Errorf("literal should expand the unset name to empty: %q", got)
	}
}

// unsetEnvVarConfig is a release with a lazy captured var whose command names
// ${env.NOPE}, referenced only by the `push` step; `NOPE` is never set. Tests
// vary which steps are in the plan to show which vars the check covers.
func unsetEnvVarConfig(t *testing.T, pushStep *StepConfig) *Config {
	t.Helper()
	return &Config{
		Order: []string{"build", "push"},
		Vars: map[string]*VarSpec{"key": {
			OS:   map[string]CommandSpec{"linux": ArgvCommand("secret-tool", "lookup", "account", "${env.NOPE}")},
			Lazy: true,
		}},
		Steps: map[string]*StepConfig{
			"build": {Run: CommandList{ShellCommand("dotnet build")}},
			"push":  pushStep,
		},
	}
}

// runUnsetEnv runs config for real and as a dry run with NOPE unset, returning
// both outcomes and their messages.
func runUnsetEnv(t *testing.T, config *Config, opts ResolveOptions) (run, dry bool, runMsg, dryMsg string) {
	t.Helper()
	env := map[string]string{"OTHER": "x"}
	reporter := &recordingReporter{}
	p := New((&recordingRunner{}).run, reporter, NewSecretMasker(), &stubPrompter{answer: true}, "/tmp/repo", env, nil, nil)
	run = p.Run(mustResolve(t, config, opts), config, false)
	dryReporter := &recordingReporter{}
	d := New((&recordingRunner{}).run, dryReporter, NewSecretMasker(), &stubPrompter{answer: true}, "/tmp/repo", env, nil, nil)
	dry = d.Run(mustResolve(t, config, opts), config, true)
	return run, dry, reporter.message, dryReporter.message
}

// A lazy var referenced only by a step that is disabled — enabled:false, or
// cut from the plan by --skip — is never resolved, so an unset ${env.NAME} in
// its command is not this run's problem: the release goes ahead, as it did
// before the check existed. An optional credential stays optional.
func TestLazyVarBehindDisabledStepIsNotChecked(t *testing.T) {
	restore := currentOSToken
	t.Cleanup(func() { currentOSToken = restore })
	currentOSToken = func() string { return "linux" }

	off := false
	disabled := unsetEnvVarConfig(t, &StepConfig{Run: CommandList{ShellCommand("nuget push --api-key ${vars.key}")}, Enabled: &off})
	if run, dry, runMsg, dryMsg := runUnsetEnv(t, disabled, ResolveOptions{}); !run || !dry {
		t.Errorf("enabled:false: run=%v (%q) dry=%v (%q), want both to succeed", run, runMsg, dry, dryMsg)
	}

	skipped := unsetEnvVarConfig(t, &StepConfig{Run: CommandList{ShellCommand("nuget push --api-key ${vars.key}")}})
	if run, dry, runMsg, dryMsg := runUnsetEnv(t, skipped, ResolveOptions{Skip: []string{"push"}}); !run || !dry {
		t.Errorf("--skip push: run=%v (%q) dry=%v (%q), want both to succeed", run, runMsg, dry, dryMsg)
	}
}

// An `if`-gated step is enabled — the gate is decided at run time and the
// step may run — so a lazy var it references is checked and the run still
// fails up front.
func TestLazyVarBehindIfGatedStepIsChecked(t *testing.T) {
	restore := currentOSToken
	t.Cleanup(func() { currentOSToken = restore })
	currentOSToken = func() string { return "linux" }

	config := unsetEnvVarConfig(t, &StepConfig{Run: CommandList{ShellCommand("nuget push --api-key ${vars.key}")}, If: ptr(`ctx.env.PUBLISH == "yes"`)})
	run, dry, runMsg, dryMsg := runUnsetEnv(t, config, ResolveOptions{})
	if run || dry {
		t.Fatalf("run=%v dry=%v, want both to fail up front behind an if-gated step", run, dry)
	}
	for _, msg := range []string{runMsg, dryMsg} {
		if !strings.Contains(msg, "variable 'key'") || !strings.Contains(msg, "${env.NOPE}") {
			t.Errorf("message %q should name the var and the env var", msg)
		}
	}
}

// A lazy var nothing in the plan references is never resolved, so it is not
// checked: defining it costs nothing.
func TestUnreferencedLazyVarIsNotChecked(t *testing.T) {
	restore := currentOSToken
	t.Cleanup(func() { currentOSToken = restore })
	currentOSToken = func() string { return "linux" }

	config := unsetEnvVarConfig(t, &StepConfig{Run: CommandList{ShellCommand("nuget push --api-key from-elsewhere")}})
	if run, dry, runMsg, dryMsg := runUnsetEnv(t, config, ResolveOptions{}); !run || !dry {
		t.Errorf("run=%v (%q) dry=%v (%q), want both to succeed with the var unreferenced", run, runMsg, dry, dryMsg)
	}
}

// An eager (non-lazy) var is captured up front whether or not a step uses it —
// the eager loop resolves every one — so its command would run and fail on the
// placeholder regardless. The check covers it even with no reference, so that
// failure is named up front instead of surfacing as an exit code.
func TestUnreferencedEagerVarIsChecked(t *testing.T) {
	restore := currentOSToken
	t.Cleanup(func() { currentOSToken = restore })
	currentOSToken = func() string { return "linux" }

	config := unsetEnvVarConfig(t, &StepConfig{Run: CommandList{ShellCommand("nuget push --api-key from-elsewhere")}})
	config.Vars["key"].Lazy = false
	run, dry, runMsg, dryMsg := runUnsetEnv(t, config, ResolveOptions{})
	if run || dry {
		t.Fatalf("run=%v dry=%v, want both to fail: an eager var is resolved whether or not it is referenced", run, dry)
	}
	for _, msg := range []string{runMsg, dryMsg} {
		if !strings.Contains(msg, "variable 'key'") || !strings.Contains(msg, "${env.NOPE}") {
			t.Errorf("message %q should name the var and the env var", msg)
		}
	}
}

func ptr[T any](v T) *T { return &v }
