package pipeline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScriptSpecStringAndArrayForms(t *testing.T) {
	str := mustParseConfig(t, `{ "steps": { "s": { "script": "log(1)" } } }`)
	if got := str.Steps["s"].Script.Code; got != "log(1)" {
		t.Errorf("string form = %q", got)
	}

	arr := mustParseConfig(t, `{ "steps": { "s": { "script": ["a := 1", "log(a)"] } } }`)
	if got := arr.Steps["s"].Script.Code; got != "a := 1\nlog(a)" {
		t.Errorf("array form = %q, want joined lines", got)
	}
}

func TestScriptSpecFileForm(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "stage.tengo"), []byte("log(`from file`)"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "release.jsonc")
	if err := os.WriteFile(path, []byte(`{ "steps": { "s": { "script": { "file": "stage.tengo" } } } }`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Steps["s"].Script.Code; got != "log(`from file`)" {
		t.Errorf("file form code = %q", got)
	}

	// A missing file is a clear error.
	bad := filepath.Join(dir, "bad.jsonc")
	if err := os.WriteFile(bad, []byte(`{ "steps": { "s": { "script": { "file": "nope.tengo" } } } }`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(bad); err == nil {
		t.Error("a missing script file should error")
	}
}

func newScriptPipeline(workDir string, relctx ReleaseContext) (*Pipeline, *recordingRunner, *recordingReporter) {
	runner := &recordingRunner{}
	reporter := &recordingReporter{}
	p := New(runner.run, reporter, NewSecretMasker(), &stubPrompter{answer: true}, workDir, nil, nil, relctx)
	return p, runner, reporter
}

func TestResolveScriptStep(t *testing.T) {
	code := `log("hi")`
	config := &Config{Order: []string{"x"}, Steps: map[string]*StepConfig{"x": {Script: &ScriptSpec{Code: code}}}}

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
		"x": {Run: CommandList{ShellCommand("echo")}, Script: &ScriptSpec{Code: code}},
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
	config := &Config{Order: []string{"s"}, Steps: map[string]*StepConfig{"s": {Script: &ScriptSpec{Code: code}}}}

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
	config := &Config{Order: []string{"s"}, Steps: map[string]*StepConfig{"s": {Script: &ScriptSpec{Code: code}}}}
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
	config := &Config{Order: []string{"s"}, Steps: map[string]*StepConfig{"s": {Script: &ScriptSpec{Code: code}}}}
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
	config := &Config{Order: []string{"s"}, Steps: map[string]*StepConfig{"s": {Script: &ScriptSpec{Code: code}}}}

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
		Steps: map[string]*StepConfig{"release": {Script: &ScriptSpec{Code: code}}},
	}
	step := mustResolve(t, config, ResolveOptions{})[0]
	if step.Kind != StepKindScript {
		t.Errorf("kind = %v, want StepKindScript", step.Kind)
	}
	if !step.OverridesNative {
		t.Error("a script on a native step should set OverridesNative")
	}
}
