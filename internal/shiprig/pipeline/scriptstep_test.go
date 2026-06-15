package pipeline

import (
	"path/filepath"
	"strings"
	"testing"
)

func newScriptPipeline(workDir string, relctx ReleaseContext) (*Pipeline, *recordingRunner, *recordingReporter) {
	runner := &recordingRunner{}
	reporter := &recordingReporter{}
	p := New(runner.run, reporter, NewSecretMasker(), &stubPrompter{answer: true}, workDir, nil, nil, relctx)
	return p, runner, reporter
}

func TestResolveScriptStep(t *testing.T) {
	code := `log("hi")`
	config := &Config{Order: []string{"x"}, Steps: map[string]*StepConfig{"x": {Script: &code}}}

	step := mustResolve(t, config, ResolveOptions{})[0]
	if step.Kind != StepKindScript {
		t.Errorf("kind = %v, want StepKindScript", step.Kind)
	}
	if step.Script != code {
		t.Errorf("script = %q", step.Script)
	}
}

func TestResolveRejectsRunAndScript(t *testing.T) {
	code := `log("hi")`
	config := &Config{Order: []string{"x"}, Steps: map[string]*StepConfig{
		"x": {Run: CommandList{ShellCommand("echo")}, Script: &code},
	}}
	if _, err := Resolve(config, ResolveOptions{}); err == nil || !strings.Contains(err.Error(), "both") {
		t.Errorf("err = %v, want a both-run-and-script error", err)
	}
}

func TestScriptStepRunsShAndFileOps(t *testing.T) {
	dir := t.TempDir()
	p, runner, _ := newScriptPipeline(dir, onePackage("1.0.0"))

	code := `
mkdir("-p", "dist")
sh("echo building " + ctx.version)
log("done")
`
	config := &Config{Order: []string{"s"}, Steps: map[string]*StepConfig{"s": {Script: &code}}}

	if !p.Run(mustResolve(t, config, ResolveOptions{}), config, false) {
		t.Fatal("script step should succeed")
	}
	if !isDir(filepath.Join(dir, "dist")) {
		t.Error("mkdir() did not create dist")
	}
	if got := strings.Join(runner.lines(), "\n"); !strings.Contains(got, "echo building 1.0.0") {
		t.Errorf("sh() not run with interpolated ctx.version: %q", got)
	}
}

func TestScriptStepFailAborts(t *testing.T) {
	p, _, reporter := newScriptPipeline(t.TempDir(), twoPackages())

	code := `fail("nope")`
	config := &Config{Order: []string{"s"}, Steps: map[string]*StepConfig{"s": {Script: &code}}}
	if p.Run(mustResolve(t, config, ResolveOptions{}), config, false) {
		t.Error("fail() should fail the run")
	}
	if reporter.success == nil || *reporter.success {
		t.Error("run should be reported failed")
	}
}

func TestScriptStepShNonZeroFails(t *testing.T) {
	runner := &recordingRunner{responder: func(recordedCommand) ([]string, int) { return []string{"boom"}, 1 }}
	reporter := &recordingReporter{}
	p := New(runner.run, reporter, NewSecretMasker(), &stubPrompter{answer: true}, t.TempDir(), nil, nil, twoPackages())

	code := `sh("do-thing")`
	config := &Config{Order: []string{"s"}, Steps: map[string]*StepConfig{"s": {Script: &code}}}
	if p.Run(mustResolve(t, config, ResolveOptions{}), config, false) {
		t.Error("a non-zero sh() should fail the run")
	}
}

func TestScriptStepDryRunPreviewsSideEffects(t *testing.T) {
	dir := t.TempDir()
	p, runner, _ := newScriptPipeline(dir, onePackage("1.0.0"))

	code := `
log("planning " + ctx.version)
mkdir("-p", "dist")
sh("publish")
`
	config := &Config{Order: []string{"s"}, Steps: map[string]*StepConfig{"s": {Script: &code}}}

	if !p.Run(mustResolve(t, config, ResolveOptions{}), config, true) {
		t.Fatal("dry run should succeed")
	}
	// The script's logic ran (it evaluated ctx) but its side effects were
	// previewed: no dir created, no command executed.
	if isDir(filepath.Join(dir, "dist")) {
		t.Error("mkdir() must not run in a dry run")
	}
	if len(runner.calls) != 0 {
		t.Errorf("sh() must not execute in a dry run; calls=%v", runner.lines())
	}
}

func TestScriptOverridesNativeStep(t *testing.T) {
	code := `log("custom release")`
	config := &Config{
		Order: []string{"release"},
		Steps: map[string]*StepConfig{"release": {Script: &code}},
	}
	step := mustResolve(t, config, ResolveOptions{})[0]
	if step.Kind != StepKindScript {
		t.Errorf("kind = %v, want StepKindScript", step.Kind)
	}
	if !step.OverridesNative {
		t.Error("a script on a native step should set OverridesNative")
	}
}
