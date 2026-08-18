package cli

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/rig/config"
	"github.com/rigsmith/rigsmith/internal/rig/stale"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// newVerifyHost builds the verify command wired to a captured buffer, with
// --dry-run on so the sequence echoes its commands instead of running them.
func newVerifyHost(t *testing.T, args ...string) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	prev := dryRun
	dryRun = true
	t.Cleanup(func() { dryRun = prev })

	cmd := newVerifyCmd()
	cmd.SetContext(context.Background())
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)
	return cmd, &buf
}

// writeGoRepo scaffolds the smallest repo where build/test/run all resolve.
func writeGoRepo(t *testing.T, root string) {
	t.Helper()
	writeTreeFile(t, root, "go.mod", "module example.com/app\n\ngo 1.26\n")
	writeTreeFile(t, root, "main.go", "package main\n\nfunc main() {}\n")
}

// touchAt stamps a file's mtime so a test can make an artifact older than its
// inputs without waiting.
func touchAt(t *testing.T, path string, age time.Duration) {
	t.Helper()
	when := time.Now().Add(-age)
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
}

// The sequence is build → test → run, in that order, through the real verbs.
func TestVerify_SequencesBuildTestRun(t *testing.T) {
	isolateGlobalConfig(t)
	root := t.TempDir()
	writeGoRepo(t, root)
	t.Chdir(root)

	cmd, buf := newVerifyHost(t)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("verify: %v (output: %s)", err, buf.String())
	}
	out := buf.String()
	build, test, run := strings.Index(out, "go build"), strings.Index(out, "go test"), strings.Index(out, "go run")
	if build < 0 || test < 0 || run < 0 {
		t.Fatalf("output is missing a step:\n%s", out)
	}
	if !(build < test && test < run) {
		t.Errorf("steps ran out of order (build=%d test=%d run=%d):\n%s", build, test, run, out)
	}
}

// --no-run stops after test, and says the step was skipped rather than
// letting a shorter sequence pass for the full one.
func TestVerify_NoRunSkipsTheRunStepOutLoud(t *testing.T) {
	isolateGlobalConfig(t)
	root := t.TempDir()
	writeGoRepo(t, root)
	t.Chdir(root)

	cmd, buf := newVerifyHost(t, "--no-run")
	if err := cmd.Execute(); err != nil {
		t.Fatalf("verify --no-run: %v (output: %s)", err, buf.String())
	}
	out := buf.String()
	if strings.Contains(out, "go run") {
		t.Errorf("--no-run still ran the run step:\n%s", out)
	}
	if !strings.Contains(out, "run — skipped") {
		t.Errorf("the skipped run step should be reported:\n%s", out)
	}
}

// `verify.run: false` in .rig.json drops the step too, and names the config as
// the reason.
func TestVerify_ConfigCanDropTheRunStep(t *testing.T) {
	isolateGlobalConfig(t)
	root := t.TempDir()
	writeGoRepo(t, root)
	writeRigJSON(t, root, `{ "ecosystem": "go", "verify": { "run": false } }`)
	t.Chdir(root)

	cmd, buf := newVerifyHost(t)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("verify: %v (output: %s)", err, buf.String())
	}
	if out := buf.String(); !strings.Contains(out, "verify.run is false") {
		t.Errorf("want the config named as the reason:\n%s", out)
	}
}

// --stale-only runs nothing: it answers "are the things I am about to trust
// built from the code I have?" against a build that may take hours.
func TestVerify_StaleOnlyRunsNothing(t *testing.T) {
	isolateGlobalConfig(t)
	root := t.TempDir()
	writeGoRepo(t, root)
	t.Chdir(root)

	cmd, buf := newVerifyHost(t, "--stale-only")
	if err := cmd.Execute(); err != nil {
		t.Fatalf("verify --stale-only: %v (output: %s)", err, buf.String())
	}
	out := buf.String()
	for _, verb := range []string{"go build", "go test", "go run"} {
		if strings.Contains(out, verb) {
			t.Errorf("--stale-only ran %q:\n%s", verb, out)
		}
	}
}

// A stale declared artifact fails the command — a warning in a long log is
// what got missed the first time.
func TestVerify_StaleArtifactIsAFailure(t *testing.T) {
	isolateGlobalConfig(t)
	root := t.TempDir()
	writeGoRepo(t, root)
	writeRigJSON(t, root, `{
  "ecosystem": "go",
  "artifacts": { "unit-tests": { "path": "out/unit_tests", "inputs": ["**/*.go"] } }
}`)
	bin := writeTreeFile(t, root, "out/unit_tests", "stale binary")
	touchAt(t, bin, 2*time.Hour)
	t.Chdir(root)

	cmd, buf := newVerifyHost(t, "--stale-only")
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("a stale artifact must fail the command (output: %s)", buf.String())
	}
	if !strings.Contains(err.Error(), "do not agree") {
		t.Errorf("err = %v, want it to say the artifacts disagree", err)
	}
	if out := buf.String(); !strings.Contains(out, "unit-tests") || !strings.Contains(out, "main.go") {
		t.Errorf("the report should name the artifact and the newer input:\n%s", out)
	}
}

// A fresh artifact passes, and the summary counts the checks that ran.
func TestVerify_FreshArtifactPasses(t *testing.T) {
	isolateGlobalConfig(t)
	root := t.TempDir()
	writeGoRepo(t, root)
	writeRigJSON(t, root, `{
  "ecosystem": "go",
  "artifacts": { "unit-tests": { "path": "out/unit_tests", "inputs": ["**/*.go"] } }
}`)
	writeTreeFile(t, root, "out/unit_tests", "fresh binary")
	t.Chdir(root)

	cmd, buf := newVerifyHost(t, "--stale-only")
	if err := cmd.Execute(); err != nil {
		t.Fatalf("verify --stale-only: %v (output: %s)", err, buf.String())
	}
	if out := buf.String(); !strings.Contains(out, "artifacts agree") {
		t.Errorf("want the agreement summary:\n%s", out)
	}
}

// Absent config is not an error: the report still runs, and says which check
// it could not perform.
func TestVerify_NoArtifactsBlockStillReportsSkips(t *testing.T) {
	isolateGlobalConfig(t)
	root := t.TempDir()
	writeGoRepo(t, root) // nothing built — the generic check can't run
	t.Chdir(root)

	cmd, buf := newVerifyHost(t, "--stale-only")
	if err := cmd.Execute(); err != nil {
		t.Fatalf("verify --stale-only: %v (output: %s)", err, buf.String())
	}
	out := buf.String()
	if !strings.Contains(out, "skipped") {
		t.Errorf("a check that did not run must say so:\n%s", out)
	}
	if !strings.Contains(out, "nothing could be checked") {
		t.Errorf("a report where nothing ran must not read as agreement:\n%s", out)
	}
}

// A bad verify.runTimeout is reported instead of silently falling back.
func TestVerify_BadRunTimeoutIsAnError(t *testing.T) {
	isolateGlobalConfig(t)
	root := t.TempDir()
	writeGoRepo(t, root)
	writeRigJSON(t, root, `{ "ecosystem": "go", "verify": { "runTimeout": "soon" } }`)
	t.Chdir(root)

	cmd, buf := newVerifyHost(t)
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "runTimeout") {
		t.Fatalf("err = %v, want a runTimeout complaint (output: %s)", err, buf.String())
	}
}

// verify must resolve a target exactly as the standalone verb does — if the two
// could disagree about what they run, verify would be worse than useless. This
// pins the reuse: each step is the same command the root tree registers.
func TestVerifyStepCmd_MatchesTheRootTreesVerbs(t *testing.T) {
	root := NewRootCmd()
	find := func(name string) *cobra.Command {
		for _, c := range root.Commands() {
			if c.Name() == name {
				return c
			}
		}
		t.Fatalf("the root tree has no %q verb", name)
		return nil
	}
	for _, verb := range []string{"build", "test", "run"} {
		step, registered := verifyStepCmd(verb), find(verb)
		if step.Name() != registered.Name() {
			t.Errorf("step %q builds %q", verb, step.Name())
		}
		registered.Flags().VisitAll(func(f *pflag.Flag) {
			if step.Flags().Lookup(f.Name) == nil {
				t.Errorf("verify's %s step is missing the verb's --%s flag", verb, f.Name)
			}
		})
	}
}

// The declared-artifact conversion tolerates a nil entry (`"name": null`) —
// a malformed config degrades, it doesn't panic.
func TestVerifyArtifacts_SkipsNilEntries(t *testing.T) {
	cfg := config.Config{Artifacts: map[string]*config.Artifact{
		"good": {Path: "out/app", Inputs: []string{"**/*.go"}},
		"nil":  nil,
	}}
	got := verifyArtifacts(cfg)
	if len(got) != 1 || got[0].Name != "good" {
		t.Fatalf("verifyArtifacts = %+v, want just the good entry", got)
	}
}

func TestRenderFindings_CountsAndMarks(t *testing.T) {
	var buf bytes.Buffer
	staleCount, skipped := renderFindings(&buf, []stale.Finding{
		{Name: "build output", Status: stale.OK, Newest: "main.go"},
		{Name: "unit-tests", Status: stale.Stale, Oldest: "out/unit_tests", Newest: "main.go",
			OldestAt: time.Now().Add(-2 * time.Hour), NewestAt: time.Now()},
		{Name: "browser", Status: stale.Skipped, Reason: "out/App.app does not exist — never built?"},
	})
	if staleCount != 1 || skipped != 1 {
		t.Fatalf("stale=%d skipped=%d, want 1 and 1", staleCount, skipped)
	}
	out := buf.String()
	for _, want := range []string{"build output", "unit-tests", "browser", "skipped", "2h older"} {
		if !strings.Contains(out, want) {
			t.Errorf("report is missing %q:\n%s", want, out)
		}
	}
}

// Every check in the report is named, so the caller can tell a pass from a
// check that never ran.
func TestAgreementFindings_UnresolvedEcosystemStillChecksArtifacts(t *testing.T) {
	isolateGlobalConfig(t)
	root := t.TempDir()
	// No manifest at all → no primary ecosystem, so the generic check can't run.
	t.Chdir(root)
	writeTreeFile(t, root, "out/app", "binary")

	cfg := config.Config{Artifacts: map[string]*config.Artifact{"app": {Path: "out/app"}}}
	findings := agreementFindings(root, cfg)
	if len(findings) != 2 {
		t.Fatalf("findings = %+v, want the skipped generic check plus the artifact", findings)
	}
	if findings[0].Name != stale.OutputCheckName || findings[0].Status != stale.Skipped {
		t.Errorf("first finding = %+v, want the generic check reported as skipped", findings[0])
	}
	if findings[1].Name != "app" {
		t.Errorf("second finding = %+v, want the declared artifact", findings[1])
	}
}

// A report that mixes a pass with a check that could not run says both — a
// green line that hid the skip is how this went wrong the first time.
func TestVerify_SummaryCountsBothPassesAndSkips(t *testing.T) {
	isolateGlobalConfig(t)
	root := t.TempDir()
	writeGoRepo(t, root)
	writeRigJSON(t, root, `{
  "ecosystem": "go",
  "artifacts": { "browser": { "path": "out/App.app", "inputs": ["**/*.go"] } }
}`)
	writeTreeFile(t, root, "bin/app", "built") // the generic check can run
	t.Chdir(root)

	cmd, buf := newVerifyHost(t, "--stale-only")
	if err := cmd.Execute(); err != nil {
		t.Fatalf("verify --stale-only: %v (output: %s)", err, buf.String())
	}
	out := buf.String()
	if !strings.Contains(out, "artifacts agree (1 check)") {
		t.Errorf("want the passing check counted:\n%s", out)
	}
	if !strings.Contains(out, "1 check could not run") {
		t.Errorf("want the unbuilt browser artifact counted as skipped:\n%s", out)
	}
}

// A .rig.json that didn't parse drops the artifacts block. Saying nothing about
// that would produce exactly the misleading quiet result verify exists to
// prevent, so the loader's complaint is surfaced.
func TestVerify_MalformedConfigIsReported(t *testing.T) {
	isolateGlobalConfig(t)
	root := t.TempDir()
	writeGoRepo(t, root)
	writeRigJSON(t, root, `{ "artifacts": { "app": { "path": `) // truncated
	t.Chdir(root)

	cmd, buf := newVerifyHost(t, "--stale-only")
	if err := cmd.Execute(); err != nil {
		t.Fatalf("verify --stale-only: %v (output: %s)", err, buf.String())
	}
	if out := buf.String(); !strings.Contains(out, "not valid JSON") {
		t.Errorf("want the unparseable config reported:\n%s", out)
	}
}
