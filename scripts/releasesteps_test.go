package scripts

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// What publishes a rigsmith release runs as a series of steps in one job:
// GoReleaser builds and publishes the GitHub release, the tap and the archives;
// then npm gets its wrapper packages; then komac submits to winget.
//
// A step is skipped when an EARLIER step failed, and `continue-on-error` does
// not change that — it only stops a step from failing the job it is in. So the
// winget submission inherited every earlier step's luck. In 1.19.0 it cost us:
// NPM_TOKEN had expired, the npm step failed, and all five winget submissions
// were silently skipped although the release was out and the manifests were
// fine. Nothing said so; the step simply reported "skipped".
//
// The fix is for each late step to name what it actually depends on. These tests
// hold that, because the failure mode of the fix is itself silent: an `if` that
// reads `steps.goreleaser.outcome` while the GoReleaser step carries no `id`
// evaluates to the empty string, which is falsey, and the step then never runs
// at all — the same invisible skip, arrived at from the other direction.

const releaseWorkflow = "../.github/workflows/goreleaser.yml"

type wfStep struct {
	Name          string `yaml:"name"`
	Uses          string `yaml:"uses"`
	ID            string `yaml:"id"`
	If            string `yaml:"if"`
	ContinueOnErr bool   `yaml:"continue-on-error"`
}

// releaseSteps reads the publishing job's steps in order.
func releaseSteps(t *testing.T) []wfStep {
	t.Helper()
	raw, err := os.ReadFile(releaseWorkflow)
	if err != nil {
		t.Fatal(err)
	}
	var wf struct {
		Jobs map[string]struct {
			Steps []wfStep `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(raw, &wf); err != nil {
		t.Fatal(err)
	}
	job, ok := wf.Jobs["goreleaser"]
	if !ok {
		t.Fatal("no `goreleaser` job in the release workflow — fix this test rather than deleting it")
	}
	if len(job.Steps) == 0 {
		t.Fatal("the release job has no steps — this test would pass vacuously")
	}
	return job.Steps
}

func stepNamed(t *testing.T, steps []wfStep, substr string) wfStep {
	t.Helper()
	for _, s := range steps {
		if strings.Contains(s.Name, substr) || strings.Contains(s.Uses, substr) {
			return s
		}
	}
	t.Fatalf("no step matching %q in %s", substr, releaseWorkflow)
	return wfStep{}
}

// The condition is written against this id. Without it the expression is empty,
// the step is skipped every time, and nothing reports a problem.
func TestGoReleaserStepCarriesTheIDLaterStepsAskAbout(t *testing.T) {
	steps := releaseSteps(t)
	var ids []string
	for _, s := range steps {
		if s.ID != "" {
			ids = append(ids, s.ID)
		}
	}
	gr := stepNamed(t, steps, "goreleaser/goreleaser-action")
	if gr.ID != "goreleaser" {
		t.Fatalf("the GoReleaser step must keep `id: goreleaser` — the winget step's condition reads "+
			"steps.goreleaser.outcome, and with no such id that expression is empty, so the step is "+
			"skipped on every release and says nothing. Ids present: %v", ids)
	}
}

// The winget submission depends on the release existing. It must not depend on
// anything that happens after the release is published.
func TestWingetSubmissionDoesNotDependOnLaterSteps(t *testing.T) {
	steps := releaseSteps(t)
	winget := stepNamed(t, steps, "Submit to winget")

	if !strings.Contains(winget.If, "steps.goreleaser.outcome") {
		t.Errorf("the winget step's `if` must name what it depends on — the release — as "+
			"steps.goreleaser.outcome. Without it the step inherits every earlier step's outcome, "+
			"which is how an expired NPM_TOKEN skipped five winget submissions in 1.19.0. Got: %q", winget.If)
	}
	// `always()` would also survive an earlier failure, but it survives
	// cancellation too: a cancelled run would go on submitting to winget.
	if strings.Contains(winget.If, "always()") {
		t.Errorf("use !cancelled() rather than always(): always() keeps submitting to winget after "+
			"someone cancels the run. Got: %q", winget.If)
	}
	if !strings.Contains(winget.If, "!cancelled()") {
		t.Errorf("the winget step's `if` should carry !cancelled() so a cancelled run stops here. Got: %q", winget.If)
	}
	// Tag-only remains part of the contract: a dry run publishes no release for
	// komac to point at.
	if !strings.Contains(winget.If, "refs/tags/v") {
		t.Errorf("the winget step must still be tag-only. Got: %q", winget.If)
	}
	// And it must stay non-fatal: the release is already out by the time it runs.
	if !winget.ContinueOnErr {
		t.Error("the winget step must keep continue-on-error: the release is already published when " +
			"it runs, and a winget submission can be re-run by hand")
	}
}

// The artifacts of a dry run are most wanted when a later step went wrong —
// which is exactly when a bare `if: github.event_name == …` withholds them.
func TestDryRunArtifactsSurviveALaterFailure(t *testing.T) {
	steps := releaseSteps(t)
	upload := stepNamed(t, steps, "actions/upload-artifact")
	if !strings.Contains(upload.If, "!cancelled()") {
		t.Errorf("the dry-run upload should carry !cancelled(), so a failure in a later step does not "+
			"withhold the artifacts someone is trying to inspect. Got: %q", upload.If)
	}
}

// A `continue-on-error` step that everything after it depends on is the trap
// this file exists for: it looks protected and is not. Any step that follows one
// must say what it depends on rather than inheriting the sequence.
func TestStepsAfterANonFatalStepNameTheirDependency(t *testing.T) {
	steps := releaseSteps(t)
	outcomeRef := regexp.MustCompile(`steps\.\w+\.outcome|!cancelled\(\)|always\(\)`)

	seenNonFatal := ""
	for _, s := range steps {
		label := s.Name
		if label == "" {
			label = s.Uses
		}
		if seenNonFatal != "" && s.If != "" && !outcomeRef.MatchString(s.If) {
			t.Errorf("step %q runs after the non-fatal step %q and its `if` (%q) names no dependency, "+
				"so it is skipped whenever that step fails — which continue-on-error makes look "+
				"impossible. Name what it needs (steps.<id>.outcome) or add !cancelled().",
				label, seenNonFatal, s.If)
		}
		if s.ContinueOnErr {
			seenNonFatal = label
		}
	}
}
