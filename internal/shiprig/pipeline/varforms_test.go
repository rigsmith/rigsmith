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
	} {
		if _, err := parseConfig(t, bad); err == nil {
			t.Errorf("%s: accepted", bad)
		}
	}
}

func ptr[T any](v T) *T { return &v }
