// Ported from net-changesets TuiReleaseReporterTests (4 contracts), asserting
// content rather than styling — lipgloss decoration varies by terminal profile.
package cli

import (
	"strings"
	"testing"

	"github.com/rigsmith/shiprig/internal/pipeline"
)

func confirmText(s string) *string { return &s }

func planSteps() []pipeline.ResolvedStep {
	return []pipeline.ResolvedStep{
		{Name: "version", IsBuiltin: true, Action: []pipeline.CommandSpec{pipeline.ShellCommand("shiprig version")}},
		{Name: "publish", IsBuiltin: true, Confirm: confirmText("Publish to registries?"),
			Action: []pipeline.CommandSpec{pipeline.ShellCommand("shiprig publish")}},
		{Name: "githubRelease", Kind: pipeline.StepKindNative},
		{Name: "docs", SkipReason: "--skip"},
	}
}

func TestRichReporterRendersPlanWithGatesAndSkips(t *testing.T) {
	var b strings.Builder
	r := newRichReporter(&b, pipeline.NewSecretMasker(), "shiprig")
	r.Plan(planSteps(), true)
	out := b.String()
	for _, want := range []string{
		"Release plan (dry run)",
		"version", "shiprig version",
		"confirm: Publish to registries?",
		"(per-package forge release)",
		"skip: --skip",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("plan output missing %q:\n%s", want, out)
		}
	}
}

func TestRichReporterFailureIncludesResumeHint(t *testing.T) {
	var b strings.Builder
	r := newRichReporter(&b, pipeline.NewSecretMasker(), "shiprig")
	r.StepStarted("version")
	r.StepCompleted("version")
	r.StepStarted("publish")
	r.CommandFailed("publish", 1)
	r.RunCompleted(false, "step 'publish' failed")
	out := b.String()
	if !strings.Contains(out, "Resume with: shiprig release --from publish") {
		t.Errorf("failure output missing the resume hint:\n%s", out)
	}
	if !strings.Contains(out, "publish") || !strings.Contains(out, "failed") {
		t.Errorf("failure output missing the failure description:\n%s", out)
	}
}

func TestRichReporterSuccessHasNoResumeHint(t *testing.T) {
	var b strings.Builder
	r := newRichReporter(&b, pipeline.NewSecretMasker(), "shiprig")
	r.StepStarted("version")
	r.StepCompleted("version")
	r.RunCompleted(true, "")
	out := b.String()
	if strings.Contains(out, "Resume with") {
		t.Errorf("success output must not carry a resume hint:\n%s", out)
	}
	if !strings.Contains(out, "Release complete") {
		t.Errorf("success output missing the success panel:\n%s", out)
	}
}

func TestRichReporterMasksSecretsEverywhere(t *testing.T) {
	masker := pipeline.NewSecretMasker()
	masker.Add("hunter2-token")
	var b strings.Builder
	r := newRichReporter(&b, masker, "shiprig")
	r.Plan([]pipeline.ResolvedStep{{Name: "publish", IsBuiltin: true,
		Action: []pipeline.CommandSpec{pipeline.ShellCommand("npm publish --otp hunter2-token")}}}, false)
	r.CommandStarted("publish", pipeline.ShellCommand("npm publish --otp hunter2-token"))
	r.CommandOutput([]string{"using token hunter2-token"})
	r.RunCompleted(false, "leak hunter2-token in message")
	out := b.String()
	if strings.Contains(out, "hunter2-token") {
		t.Fatalf("secret leaked into reporter output:\n%s", out)
	}
	if !strings.Contains(out, pipeline.MaskPlaceholder) {
		t.Errorf("masked placeholder missing:\n%s", out)
	}
}
