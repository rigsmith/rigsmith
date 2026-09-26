package scripts

import (
	"fmt"
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
	cliWorkflow = "../.github/workflows/release.yml"
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
// release for komac to point at. The release now runs on the push to main that
// tagged it, not on the tag, so "tag-only" is the step requiring RELEASE_TAG,
// and RELEASE_TAG being empty for a dry run.
func TestCLIWingetSubmissionIsTagOnly(t *testing.T) {
	winget := stepNamed(t, workflowSteps(t, cliWorkflow), "Submit to winget")
	if !strings.Contains(winget.If, "env.RELEASE_TAG != ''") {
		t.Errorf("the winget step must run only for a release tag (env.RELEASE_TAG != ''). Got: %q", winget.If)
	}
	raw, err := os.ReadFile(cliWorkflow)
	if err != nil {
		t.Fatal(err)
	}
	var wf struct {
		Jobs map[string]struct {
			Env map[string]string `yaml:"env"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(raw, &wf); err != nil {
		t.Fatal(err)
	}
	tag := wf.Jobs["goreleaser"].Env["RELEASE_TAG"]
	if tag == "" {
		t.Fatal("the goreleaser job sets no RELEASE_TAG; the winget step's condition would name nothing")
	}
	// A manual run carries a tag only in release-tag mode; a dry run must
	// leave it empty, or it would submit to winget.
	if !strings.Contains(tag, "inputs.mode == 'release-tag'") {
		t.Errorf("RELEASE_TAG = %q; a manual run should only carry a tag in release-tag mode, "+
			"so a dry run never reaches winget", tag)
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

// The release is one workflow whose jobs hand a tag along: shiprig-action's
// published-packages output → the release job's cli-tag step → its `tag`
// output → RELEASE_TAG in the build and npm jobs. Every link is a string in
// YAML, so a renamed step id or output breaks it without failing anything: the
// tag comes out empty and the build jobs quietly don't run. This holds the
// links, and the gates around them, in place.
func TestReleaseWorkflowWiring(t *testing.T) {
	raw, err := os.ReadFile(cliWorkflow)
	if err != nil {
		t.Fatal(err)
	}
	type step struct {
		ID   string            `yaml:"id"`
		Name string            `yaml:"name"`
		Uses string            `yaml:"uses"`
		Run  string            `yaml:"run"`
		Env  map[string]string `yaml:"env"`
		With map[string]string `yaml:"with"`
	}
	type job struct {
		If      string            `yaml:"if"`
		Needs   any               `yaml:"needs"`
		Outputs map[string]string `yaml:"outputs"`
		Env     map[string]string `yaml:"env"`
		Steps   []step            `yaml:"steps"`
	}
	var wf struct {
		On struct {
			Push struct {
				Branches []string `yaml:"branches"`
				Tags     []string `yaml:"tags"`
			} `yaml:"push"`
		} `yaml:"on"`
		Jobs map[string]job `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(raw, &wf); err != nil {
		t.Fatal(err)
	}
	needs := func(j job) string { return fmt.Sprint(j.Needs) }
	stepIndex := func(j job, pred func(step) bool) int {
		for i, s := range j.Steps {
			if pred(s) {
				return i
			}
		}
		return -1
	}

	// Triggers: pushes to main (the release PR and its tags), and vX.Y.Z tags
	// pushed by hand.
	if strings.Join(wf.On.Push.Branches, ",") != "main" || strings.Join(wf.On.Push.Tags, ",") != "v*" {
		t.Errorf("push triggers = branches %v, tags %v; want [main] and [v*]", wf.On.Push.Branches, wf.On.Push.Tags)
	}

	release, ok := wf.Jobs["release"]
	if !ok {
		t.Fatal("no release job")
	}
	action := stepIndex(release, func(s step) bool { return strings.HasPrefix(s.Uses, "rigsmith/shiprig-action@") })
	if action < 0 || release.Steps[action].ID != "shiprig" {
		t.Fatalf("the release job's shiprig-action step is missing or not id: shiprig")
	}
	if release.Steps[action].With["publish-script"] != "shiprig tag" {
		t.Errorf("publish-script = %q, want \"shiprig tag\"", release.Steps[action].With["publish-script"])
	}
	// Tag only what CI passed: the wait comes before the step that tags.
	wait := stepIndex(release, func(s step) bool { return strings.Contains(s.Run, "gh run list --workflow ci.yml") })
	if wait < 0 || wait > action {
		t.Errorf("the CI wait (step %d) must come before shiprig-action (step %d)", wait, action)
	}
	// shiprig-action v0.5.0 sets published-packages (hyphenated; its src/index.ts).
	cli := stepIndex(release, func(s step) bool { return s.ID == "cli-tag" })
	if cli < 0 {
		t.Fatal("no cli-tag step in the release job")
	}
	c := release.Steps[cli]
	if c.Env["PUBLISHED"] != "${{ steps.shiprig.outputs.published-packages }}" {
		t.Errorf("cli-tag reads %q, want shiprig-action's published-packages output", c.Env["PUBLISHED"])
	}
	if !strings.Contains(c.Run, `.name == "github.com/rigsmith/rigsmith"`) || !strings.Contains(c.Run, `tag=v$version`) {
		t.Errorf("cli-tag should pick the root module and write tag=v<version>:\n%s", c.Run)
	}
	if release.Outputs["tag"] != "${{ steps.cli-tag.outputs.tag }}" {
		t.Errorf("release job output tag = %q, want the cli-tag step's", release.Outputs["tag"])
	}

	for _, name := range []string{"goreleaser", "npm"} {
		j, ok := wf.Jobs[name]
		if !ok {
			t.Fatalf("no %s job", name)
		}
		for _, dep := range []string{"release", "pushed-tag"} {
			if !strings.Contains(needs(j), dep) {
				t.Errorf("%s needs %s, got %s", name, dep, needs(j))
			}
		}
		tag := j.Env["RELEASE_TAG"]
		for _, src := range []string{"needs.release.outputs.tag", "needs.pushed-tag.outputs.tag"} {
			if !strings.Contains(tag, src) {
				t.Errorf("%s's RELEASE_TAG = %q; it should take %s", name, tag, src)
			}
		}
	}
	if !strings.Contains(needs(wf.Jobs["npm"]), "goreleaser") {
		t.Errorf("npm needs goreleaser, got %s", needs(wf.Jobs["npm"]))
	}

	// The GitHub release's notes are the changelog entry.
	gr := wf.Jobs["goreleaser"]
	notes := stepIndex(gr, func(s step) bool { return strings.Contains(s.Run, "scripts/release-notes.sh") })
	build := stepIndex(gr, func(s step) bool { return strings.HasPrefix(s.Uses, "goreleaser/goreleaser-action@") })
	if notes < 0 || build < 0 || notes > build {
		t.Errorf("release-notes.sh (step %d) must run before GoReleaser (step %d)", notes, build)
	} else if !strings.Contains(gr.Steps[build].With["args"], "--release-notes=") {
		t.Errorf("GoReleaser's args don't pass --release-notes: %q", gr.Steps[build].With["args"])
	}
	// A build checks CI passed on its tag's commit, before anything is built.
	ci := stepIndex(gr, func(s step) bool { return strings.Contains(s.Run, "gh run list --workflow ci.yml") })
	checkout := stepIndex(gr, func(s step) bool { return strings.HasPrefix(s.Uses, "actions/checkout@") })
	if ci < 0 || ci > checkout {
		t.Errorf("the build's CI check (step %d) must come before checkout (step %d)", ci, checkout)
	}
}
