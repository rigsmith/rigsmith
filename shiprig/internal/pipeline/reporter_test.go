// Ported from net-changesets Release/PlainReleaseReporterTests.cs (2 tests)
// and Release/PlanChooserTests.cs (1 test).
package pipeline

import (
	"strings"
	"testing"
)

func TestPlainReporterFailureAfterAStepPrintsResumeHint(t *testing.T) {
	var out strings.Builder
	reporter := NewPlainReporter(&out, NewSecretMasker(), "")

	reporter.StepStarted("publish")
	reporter.RunCompleted(false, "step 'publish' failed")

	if !strings.Contains(out.String(), "Release failed") {
		t.Errorf("output missing failure line: %q", out.String())
	}
	if !strings.Contains(out.String(), "--from publish") {
		t.Errorf("output missing resume hint: %q", out.String())
	}
}

func TestPlainReporterSuccessPrintsNoResumeHint(t *testing.T) {
	var out strings.Builder
	reporter := NewPlainReporter(&out, NewSecretMasker(), "")

	reporter.RunCompleted(true, "")

	if !strings.Contains(out.String(), "Release complete") {
		t.Errorf("output missing success line: %q", out.String())
	}
	if strings.Contains(out.String(), "--from") {
		t.Errorf("success output must not include a resume hint: %q", out.String())
	}
}

func TestPassthroughChooserReturnsStepsUnchanged(t *testing.T) {
	steps := mustResolve(t, &Config{}, ResolveOptions{})

	chosen, proceed := PassthroughChooser{}.Choose(steps)

	if !proceed {
		t.Error("passthrough chooser must always proceed")
	}
	if len(chosen) != len(steps) || &chosen[0] != &steps[0] {
		t.Error("passthrough chooser must return the same steps slice")
	}
}
