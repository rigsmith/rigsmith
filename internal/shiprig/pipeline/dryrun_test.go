package pipeline

import (
	"strings"
	"testing"
)

// plannedStep finds a step by name in what the reporter was handed for the plan.
func plannedStep(t *testing.T, r *recordingReporter, name string) ResolvedStep {
	t.Helper()
	for _, s := range r.plannedSteps {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("step %q not in planned steps", name)
	return ResolvedStep{}
}

func TestDryRunSpecUnmarshalForms(t *testing.T) {
	cfg := mustParseConfig(t, `{
		"steps": {
			"a": { "run": "x", "dryRun": true },
			"b": { "run": "x", "dryRun": false },
			"c": { "run": "x", "dryRun": "x --dry-run" },
			"d": { "run": "x", "dryRun": ["one", "two"] }
		}
	}`)

	if d := cfg.Steps["a"].DryRun; d == nil || !d.Execute || d.Hide || d.Commands != nil {
		t.Errorf("dryRun true = %+v", d)
	}
	if d := cfg.Steps["b"].DryRun; d == nil || d.Execute || !d.Hide {
		t.Errorf("dryRun false = %+v", d)
	}
	if d := cfg.Steps["c"].DryRun; d == nil || !d.Execute || len(d.Commands) != 1 {
		t.Errorf("dryRun string = %+v", d)
	}
	if d := cfg.Steps["d"].DryRun; d == nil || len(d.Commands) != 2 {
		t.Errorf("dryRun array = %+v", d)
	}
}

func dryFixture(relctx ReleaseContext) (*Pipeline, *recordingRunner, *recordingReporter) {
	runner := &recordingRunner{}
	reporter := &recordingReporter{}
	p := New(runner.run, reporter, NewSecretMasker(), &stubPrompter{answer: true}, "/tmp/repo", nil, nil, relctx)
	return p, runner, reporter
}

func TestDryRunPreviewInterpolatesAndPlaceholders(t *testing.T) {
	p, runner, reporter := dryFixture(twoPackages())
	config := &Config{
		Order: []string{"notify"},
		Vars:  map[string]*VarSpec{"otp": {Command: specPtr(ArgvCommand("op", "otp"))}},
		Steps: map[string]*StepConfig{
			"notify": {Run: CommandList{ShellCommand("echo ${versions} ${vars.otp} ${releaseUrl.web}")}},
		},
	}

	if !p.Run(mustResolve(t, config, ResolveOptions{}), config, true) {
		t.Fatal("dry run should succeed")
	}

	line := DescribeCommand(plannedStep(t, reporter, "notify").Action[0])
	if !strings.Contains(line, "@acme/web@2.1.0, acme/cli@1.4.0") {
		t.Errorf("planned line missing interpolated versions: %q", line)
	}
	if !strings.Contains(line, "‹vars.otp›") || !strings.Contains(line, "‹releaseUrl.web›") {
		t.Errorf("planned line missing placeholders: %q", line)
	}
	if len(runner.calls) != 0 {
		t.Errorf("dry run must not execute or capture anything; calls=%v", runner.lines())
	}
}

func TestDryRunExecutesOptedInActionWithoutCapturingVars(t *testing.T) {
	p, runner, _ := dryFixture(twoPackages())
	config := &Config{
		Order: []string{"smoke"},
		Vars:  map[string]*VarSpec{"otp": {Command: specPtr(ArgvCommand("op", "otp"))}},
		Steps: map[string]*StepConfig{
			"smoke": {
				Run:    CommandList{ShellCommand("./smoke.sh ${vars.otp}")},
				DryRun: &DryRunSpec{Execute: true},
			},
		},
	}

	if !p.Run(mustResolve(t, config, ResolveOptions{}), config, true) {
		t.Fatal("dry run should succeed")
	}

	if len(runner.calls) != 1 {
		t.Fatalf("expected the opted-in action to run once; calls=%v", runner.lines())
	}
	got := runner.calls[0].args[0]
	if !strings.Contains(got, "./smoke.sh ‹vars.otp›") {
		t.Errorf("dry action = %q, want the var placeholdered (not captured)", got)
	}
}

func TestDryRunAlternateRunsInsteadOfAction(t *testing.T) {
	p, runner, _ := dryFixture(nil)
	config := &Config{
		Order: []string{"publish"},
		Steps: map[string]*StepConfig{
			"publish": {DryRun: &DryRunSpec{Execute: true, Commands: CommandList{ShellCommand("changeset publish --dry-run")}}},
		},
	}

	if !p.Run(mustResolve(t, config, ResolveOptions{}), config, true) {
		t.Fatal("dry run should succeed")
	}

	if len(runner.calls) != 1 || !strings.Contains(runner.calls[0].args[0], "publish --dry-run") {
		t.Errorf("expected only the alternate to run; calls=%v", runner.lines())
	}
}

func TestDryRunVersionPreviewsChangelogByDefault(t *testing.T) {
	p, runner, _ := dryFixture(nil)
	config := &Config{Order: []string{"version"}}

	if !p.Run(mustResolve(t, config, ResolveOptions{}), config, true) {
		t.Fatal("dry run should succeed")
	}

	if len(runner.calls) != 1 {
		t.Fatalf("version step should preview its changelog once; calls=%v", runner.lines())
	}
	if got := runner.calls[0].args[0]; !strings.Contains(got, "changeset version --changelog") {
		t.Errorf("version dry action = %q, want a `version --changelog` preview", got)
	}
}

func TestDryRunVersionPreviewHonorsToolAndArgs(t *testing.T) {
	p, runner, _ := dryFixture(nil)
	config := &Config{
		Tool:  "shiprig",
		Order: []string{"version"},
		Steps: map[string]*StepConfig{"version": {Args: []string{"--snapshot"}}},
	}

	if !p.Run(mustResolve(t, config, ResolveOptions{}), config, true) {
		t.Fatal("dry run should succeed")
	}

	if got := runner.calls[0].args[0]; !strings.Contains(got, "shiprig version --changelog --snapshot") {
		t.Errorf("version dry action = %q, want the configured tool and args", got)
	}
}

func TestDryRunVersionExternalToolSkipsPreview(t *testing.T) {
	p, runner, _ := dryFixture(nil)
	// "npx changeset" backs the built-in steps with the Node @changesets/cli,
	// whose `version` has no `--changelog`; the preview would fail, so skip it.
	config := &Config{Tool: "npx changeset", Order: []string{"version"}}

	if !p.Run(mustResolve(t, config, ResolveOptions{}), config, true) {
		t.Fatal("dry run should succeed")
	}

	if len(runner.calls) != 0 {
		t.Errorf("external tool must not auto-preview --changelog; calls=%v", runner.lines())
	}
}

func TestDryRunVersionCustomRunSuppressesDefaultPreview(t *testing.T) {
	p, runner, _ := dryFixture(nil)
	config := &Config{
		Order: []string{"version"},
		// A custom run replaces the built-in version action, so its changelog
		// preview no longer applies; a plain dry run must execute nothing.
		Steps: map[string]*StepConfig{"version": {Run: CommandList{ShellCommand("./my-version.sh")}}},
	}

	if !p.Run(mustResolve(t, config, ResolveOptions{}), config, true) {
		t.Fatal("dry run should succeed")
	}

	if len(runner.calls) != 0 {
		t.Errorf("custom-run version must not auto-preview; calls=%v", runner.lines())
	}
}

func TestDryRunVersionExplicitDryRunOverridesDefaultPreview(t *testing.T) {
	p, runner, _ := dryFixture(nil)
	// An explicit "dryRun": false on the step hides its action; the default
	// changelog preview must not slip back in.
	config := mustParseConfig(t, `{"order": ["version"], "steps": {"version": {"dryRun": false}}}`)

	if !p.Run(mustResolve(t, config, ResolveOptions{}), config, true) {
		t.Fatal("dry run should succeed")
	}

	if len(runner.calls) != 0 {
		t.Errorf(`"dryRun": false should suppress the default preview; calls=%v`, runner.lines())
	}
}

func TestDryRunFalseHidesActionAndRunsNothing(t *testing.T) {
	p, runner, reporter := dryFixture(nil)
	config := &Config{
		Order: []string{"publish"},
		Steps: map[string]*StepConfig{
			"publish": {DryRun: &DryRunSpec{Hide: true}},
		},
	}

	if !p.Run(mustResolve(t, config, ResolveOptions{}), config, true) {
		t.Fatal("dry run should succeed")
	}
	if len(runner.calls) != 0 {
		t.Errorf("hidden action must not run; calls=%v", runner.lines())
	}
	if action := plannedStep(t, reporter, "publish").Action; action != nil {
		t.Errorf("hidden action should be omitted from the plan, got %v", action)
	}
}
