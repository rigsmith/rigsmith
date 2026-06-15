package pipeline

import (
	"strings"
	"testing"
)

func TestVarLiteralBareStringShorthand(t *testing.T) {
	cfg := mustParseConfig(t, `{ "vars": { "basePath": "/opt/acme" } }`)

	spec := cfg.Vars["basePath"]
	if spec == nil || spec.Value == nil || *spec.Value != "/opt/acme" {
		t.Fatalf("basePath spec = %+v", spec)
	}
	if spec.Command != nil {
		t.Error("a bare-string var must have no command")
	}
}

func TestVarLiteralObjectForm(t *testing.T) {
	cfg := mustParseConfig(t, `{ "vars": { "channel": { "value": "stable" } } }`)

	if v := cfg.Vars["channel"]; v == nil || v.Value == nil || *v.Value != "stable" {
		t.Fatalf("channel = %+v", v)
	}
}

func TestVarRejectsBothValueAndCommand(t *testing.T) {
	_, err := parseConfig(t, `{ "vars": { "x": { "value": "a", "command": "echo b" } } }`)
	if err == nil || !strings.Contains(err.Error(), "both") {
		t.Errorf("err = %v, want a 'both' error", err)
	}
}

func TestVarRejectsNeitherValueNorCommand(t *testing.T) {
	_, err := parseConfig(t, `{ "vars": { "x": { "lazy": true } } }`)
	if err == nil || !strings.Contains(err.Error(), "either") {
		t.Errorf("err = %v, want an 'either' error", err)
	}
}

func TestRunInterpolatesLiteralVarUnmaskedAndReused(t *testing.T) {
	runner := &recordingRunner{}
	masker := NewSecretMasker()
	p := New(runner.run, &recordingReporter{}, masker, &stubPrompter{answer: true}, "/tmp/repo", nil, nil, nil)

	basePath := "/opt/acme"
	config := &Config{
		Order: []string{"a", "b"},
		Vars:  map[string]*VarSpec{"basePath": {Value: &basePath}},
		Steps: map[string]*StepConfig{
			"a": {Run: CommandList{ShellCommand("deploy ${vars.basePath}/a")}},
			"b": {Run: CommandList{ShellCommand("deploy ${vars.basePath}/b")}},
		},
	}

	if !p.Run(mustResolve(t, config, ResolveOptions{}), config, false) {
		t.Fatal("run should succeed")
	}

	joined := strings.Join(runner.lines(), "\n")
	if !strings.Contains(joined, "deploy /opt/acme/a") || !strings.Contains(joined, "deploy /opt/acme/b") {
		t.Errorf("literal not reused across both steps: %q", joined)
	}
	// A literal is config, not a secret: it must not be registered for masking.
	if masker.Mask("/opt/acme") != "/opt/acme" {
		t.Error("a literal var must not be masked")
	}
}

func TestDryRunShowsLiteralVarButPlaceholdersCaptured(t *testing.T) {
	runner := &recordingRunner{}
	reporter := &recordingReporter{}
	p := New(runner.run, reporter, NewSecretMasker(), &stubPrompter{answer: true}, "/tmp/repo", nil, nil, nil)

	basePath := "/opt/acme"
	config := &Config{
		Order: []string{"deploy"},
		Vars: map[string]*VarSpec{
			"basePath": {Value: &basePath},
			"token":    {Command: specPtr(ArgvCommand("op", "token"))},
		},
		Steps: map[string]*StepConfig{
			"deploy": {Run: CommandList{ShellCommand("deploy ${vars.basePath} --token ${vars.token}")}},
		},
	}

	if !p.Run(mustResolve(t, config, ResolveOptions{}), config, true) {
		t.Fatal("dry run should succeed")
	}

	line := DescribeCommand(plannedStep(t, reporter, "deploy").Action[0])
	if !strings.Contains(line, "deploy /opt/acme") {
		t.Errorf("literal should be shown in the dry-run preview: %q", line)
	}
	if !strings.Contains(line, "‹vars.token›") {
		t.Errorf("captured var should stay a placeholder: %q", line)
	}
	if len(runner.calls) != 0 {
		t.Errorf("dry run must not capture anything; calls=%v", runner.lines())
	}
}
