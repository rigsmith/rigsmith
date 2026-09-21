package scripts

import (
	"os"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// Publishing a release runs as a series of steps in one job: GoReleaser builds
// and publishes the GitHub release, the tap and the archives; then npm gets its
// wrapper packages; then komac submits to winget. The UI release has the same
// shape — release, Homebrew cask, winget.
//
// A step is skipped when an EARLIER step failed, and `continue-on-error` does
// not change that. It only stops a step from failing the job it is in: a step
// that fails with continue-on-error ends with conclusion `success`, so what it
// protects is everything AFTER it, never itself from what came before.
//
// So each late step inherited every earlier step's luck. In 1.19.0 that cost
// five winget submissions: NPM_TOKEN had expired, the npm step failed, and the
// komac step reported "skipped" while the release sat published with manifests
// that turned out to need no changes at all.
//
// The fix is for the winget submission to depend on what it actually needs — a
// release with assets — and on nothing else. These tests hold that in both
// workflows.

const (
	cliWorkflow = "../.github/workflows/goreleaser.yml"
	uiWorkflow  = "../.github/workflows/release-ui.yml"
)

type wfStep struct {
	Name          string `yaml:"name"`
	Uses          string `yaml:"uses"`
	ID            string `yaml:"id"`
	If            string `yaml:"if"`
	Run           string `yaml:"run"`
	ContinueOnErr bool   `yaml:"continue-on-error"`
}

// workflowSteps reads every step of every job, in order.
func workflowSteps(t *testing.T, path string) []wfStep {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: %v — fix this test rather than deleting it", path, err)
	}
	var wf struct {
		Jobs map[string]struct {
			Steps []wfStep `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(raw, &wf); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	var all []wfStep
	for _, job := range wf.Jobs {
		all = append(all, job.Steps...)
	}
	if len(all) == 0 {
		t.Fatalf("%s has no steps — this test would pass vacuously", path)
	}
	return all
}

func stepNamed(t *testing.T, steps []wfStep, substr string) wfStep {
	t.Helper()
	for _, s := range steps {
		if strings.Contains(s.Name, substr) || strings.Contains(s.Uses, substr) {
			return s
		}
	}
	t.Fatalf("no step matching %q", substr)
	return wfStep{}
}

// The winget submission is an independent channel. Whatever else a release run
// does — npm, a Homebrew cask, a Scoop bucket — must not decide whether winget
// gets its update.
func TestWingetSubmissionIsIndependentOfOtherChannels(t *testing.T) {
	for _, path := range []string{cliWorkflow, uiWorkflow} {
		t.Run(path, func(t *testing.T) {
			winget := stepNamed(t, workflowSteps(t, path), "Submit to winget")

			if !strings.Contains(winget.If, "!cancelled()") {
				t.Errorf("the winget step's `if` is %q; without !cancelled() it inherits every "+
					"earlier step's outcome, which is how an expired NPM_TOKEN skipped five "+
					"winget submissions in 1.19.0", winget.If)
			}
			// always() would also survive an earlier failure — and a cancellation,
			// so a cancelled run would go on submitting to winget.
			if strings.Contains(winget.If, "always()") {
				t.Errorf("use !cancelled() rather than always(): always() keeps submitting after "+
					"someone cancels the run. Got: %q", winget.If)
			}
			// Naming another channel's step would reintroduce the coupling in a
			// new spelling — including GoReleaser's aggregate outcome, which
			// fails as a whole when a tap or bucket push fails, long after the
			// release and its archives are out.
			for _, other := range []string{"npm", "cask", "homebrew", "scoop", "goreleaser"} {
				if strings.Contains(strings.ToLower(winget.If), "steps."+other) {
					t.Errorf("the winget step's `if` names the %s step (%q) — that is another "+
						"publishing channel, and winget must not depend on it", other, winget.If)
				}
			}
			if !winget.ContinueOnErr {
				t.Error("the winget step must keep continue-on-error: the release is already " +
					"published when it runs, and a submission can be re-run by hand")
			}
			// What it DOES depend on: a release with assets, checked in the body.
			if !strings.Contains(winget.Run, "gh release view") || !strings.Contains(winget.Run, "assets") {
				t.Errorf("the winget step should verify the release has assets before submitting, "+
					"so a partially failed release still reaches winget and an empty one does not "+
					"download komac for nothing. Body:\n%s", winget.Run)
			}
		})
	}
}

// Tag-only remains part of the CLI contract: a manual dry run publishes no
// release for komac to point at.
func TestCLIWingetSubmissionIsTagOnly(t *testing.T) {
	winget := stepNamed(t, workflowSteps(t, cliWorkflow), "Submit to winget")
	if !strings.Contains(winget.If, "refs/tags/v") {
		t.Errorf("the winget step must stay tag-only. Got: %q", winget.If)
	}
}

// The artifacts of a dry run are most wanted when a later step went wrong —
// which is exactly when a bare `if: github.event_name == …` withholds them.
func TestDryRunArtifactsSurviveALaterFailure(t *testing.T) {
	upload := stepNamed(t, workflowSteps(t, cliWorkflow), "actions/upload-artifact")
	if !strings.Contains(upload.If, "!cancelled()") {
		t.Errorf("the dry-run upload should carry !cancelled(), so a failure in a later step does "+
			"not withhold the artifacts someone is trying to inspect. Got: %q", upload.If)
	}
}

// Every step that publishes to a channel of its own must say what it depends
// on, rather than inheriting the sequence it happens to sit in.
//
// Deliberately a list rather than a structural rule. An earlier version of this
// test flagged any step following a continue-on-error one, which was simply
// wrong: continue-on-error ends with conclusion `success`, so such a step skips
// nothing after it. And the true structural rule — "a step after anything that
// can fail the job" — describes almost every step in every workflow, including
// all the ones that SHOULD stop when checkout fails. Which steps are
// independent channels is a fact about what they do, so it is written down.
func TestEveryPublishingChannelNamesItsOwnDependency(t *testing.T) {
	channels := map[string][]string{
		cliWorkflow: {"Submit to winget"},
		uiWorkflow:  {"Submit to winget"},
	}
	for path, names := range channels {
		steps := workflowSteps(t, path)
		for _, name := range names {
			s := stepNamed(t, steps, name)
			if s.If == "" {
				t.Errorf("%s: step %q publishes to its own channel but carries no `if`, so any "+
					"earlier failure skips it", path, name)
				continue
			}
			if !strings.Contains(s.If, "!cancelled()") && !strings.Contains(s.If, "steps.") {
				t.Errorf("%s: step %q has `if: %s`, which names no dependency of its own",
					path, name, s.If)
			}
		}
	}
}
