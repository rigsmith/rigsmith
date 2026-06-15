package pipeline

import (
	"strings"
	"testing"
)

func onePackage(version string) fakeRelCtx {
	return fakeRelCtx{pkgs: []ReleasePackage{
		{Name: "@acme/web", Key: "web", Ecosystem: "node", Version: version, Tag: "@acme/web@" + version},
	}}
}

func TestBuildScriptCtx(t *testing.T) {
	ctx := buildScriptCtx(twoPackages(), map[string]string{"CI": "true"}, true)

	if ctx["dryRun"] != true {
		t.Error("dryRun should be true")
	}
	if ctx["versions"] != "@acme/web@2.1.0, acme/cli@1.4.0" {
		t.Errorf("versions = %v", ctx["versions"])
	}
	if pkgs, _ := ctx["packages"].([]interface{}); len(pkgs) != 2 {
		t.Errorf("packages len = %d, want 2", len(pkgs))
	}
	if env, _ := ctx["env"].(map[string]interface{}); env["CI"] != "true" {
		t.Errorf("env.CI = %v", ctx["env"])
	}
	// A multi-package release has no scalar ctx.version.
	if _, ok := ctx["version"]; ok {
		t.Error("multi-package release should not set a scalar ctx.version")
	}

	// Single package exposes the scalar conveniences.
	if v := buildScriptCtx(onePackage("1.2.0"), nil, false)["version"]; v != "1.2.0" {
		t.Errorf("single-package ctx.version = %v", v)
	}
}

func TestEvalScriptBoolAndString(t *testing.T) {
	ctx := buildScriptCtx(onePackage("2.0.0-beta.1"), nil, false)

	if b, err := evalScriptBool(`len(ctx.packages) == 1`, ctx); err != nil || !b {
		t.Errorf("len == 1: %v %v", b, err)
	}
	// Truthiness: a non-empty string is truthy.
	if b, _ := evalScriptBool(`ctx.version`, ctx); !b {
		t.Error("a non-empty string should be truthy")
	}
	// Ternary + a pre-bound stdlib module, no import needed.
	got, err := evalScriptString(`text.re_match("-beta", ctx.version) ? "next" : "latest"`, ctx)
	if err != nil || got != "next" {
		t.Errorf("ternary = %q (%v), want next", got, err)
	}
}

func TestEvalScriptErrorSurfaces(t *testing.T) {
	if _, err := evalScriptBool(`this is +/ not valid`, map[string]interface{}{}); err == nil {
		t.Error("a malformed expression should error")
	}
}

func stepWasSkipped(r *recordingReporter, name, reason string) bool {
	for _, s := range r.skippedSteps {
		if s.name == name && s.reason == reason {
			return true
		}
	}
	return false
}

func TestStepIfGatesExecution(t *testing.T) {
	runner := &recordingRunner{}
	reporter := &recordingReporter{}
	p := New(runner.run, reporter, NewSecretMasker(), &stubPrompter{answer: true}, "/tmp/repo", nil, nil, twoPackages())

	skip := "len(ctx.packages) > 5"
	run := "len(ctx.packages) > 0"
	config := &Config{
		Order: []string{"a", "b"},
		Steps: map[string]*StepConfig{
			"a": {If: &skip, Run: CommandList{ShellCommand("echo A")}},
			"b": {If: &run, Run: CommandList{ShellCommand("echo B")}},
		},
	}

	if !p.Run(mustResolve(t, config, ResolveOptions{}), config, false) {
		t.Fatal("run should succeed")
	}
	if !stepWasSkipped(reporter, "a", "if condition is false") {
		t.Errorf("step a should be skipped; skips=%v", reporter.skippedSteps)
	}
	joined := strings.Join(runner.lines(), "\n")
	if strings.Contains(joined, "echo A") {
		t.Error("a's action must not run")
	}
	if !strings.Contains(joined, "echo B") {
		t.Error("b's action should run")
	}
}

func TestStepIfErrorFailsRun(t *testing.T) {
	reporter := &recordingReporter{}
	p := New((&recordingRunner{}).run, reporter, NewSecretMasker(), &stubPrompter{answer: true}, "/tmp/repo", nil, nil, twoPackages())

	bad := "1 +" // a malformed expression — compile error
	config := &Config{
		Order: []string{"x"},
		Steps: map[string]*StepConfig{"x": {If: &bad, Run: CommandList{ShellCommand("echo x")}}},
	}
	if p.Run(mustResolve(t, config, ResolveOptions{}), config, false) {
		t.Error("an erroring if-condition should fail the run")
	}
}

func TestComputedVarFromScript(t *testing.T) {
	runner := &recordingRunner{}
	reporter := &recordingReporter{}
	p := New(runner.run, reporter, NewSecretMasker(), &stubPrompter{answer: true}, "/tmp/repo", nil, nil, onePackage("1.2.0"))

	expr := `ctx.version == "1.2.0" ? "stable" : "next"`
	config := &Config{
		Order: []string{"notify"},
		Vars:  map[string]*VarSpec{"channel": {Script: &expr}},
		Steps: map[string]*StepConfig{
			"notify": {Run: CommandList{ShellCommand("publish --tag ${vars.channel}")}},
		},
	}

	if !p.Run(mustResolve(t, config, ResolveOptions{}), config, false) {
		t.Fatal("run should succeed")
	}
	if got := strings.Join(runner.lines(), "\n"); !strings.Contains(got, "publish --tag stable") {
		t.Errorf("computed var not interpolated: %q", got)
	}
}

func TestComputedVarShownInDryRunPreview(t *testing.T) {
	runner := &recordingRunner{}
	reporter := &recordingReporter{}
	p := New(runner.run, reporter, NewSecretMasker(), &stubPrompter{answer: true}, "/tmp/repo", nil, nil, onePackage("3.0.0"))

	expr := `"v" + ctx.version`
	config := &Config{
		Order: []string{"notify"},
		Vars:  map[string]*VarSpec{"label": {Script: &expr}},
		Steps: map[string]*StepConfig{
			"notify": {Run: CommandList{ShellCommand("echo ${vars.label}")}},
		},
	}

	if !p.Run(mustResolve(t, config, ResolveOptions{}), config, true) { // dry run
		t.Fatal("dry run should succeed")
	}
	line := DescribeCommand(plannedStep(t, reporter, "notify").Action[0])
	if !strings.Contains(line, "echo v3.0.0") {
		t.Errorf("computed var should be shown in the dry-run preview: %q", line)
	}
	if len(runner.calls) != 0 {
		t.Errorf("a pure computed var must not execute anything; calls=%v", runner.lines())
	}
}

func TestVarRejectsValueAndScriptTogether(t *testing.T) {
	_, err := parseConfig(t, `{ "vars": { "x": { "value": "a", "script": "1" } } }`)
	if err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Errorf("err = %v, want an 'exactly one' error", err)
	}
}
