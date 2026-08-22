package doctorui

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/brand"
	"github.com/rigsmith/rigsmith/core/doctor"
	"github.com/spf13/cobra"
)

// fixture is one OK row plus one failing row that carries a Fix (which records
// that it ran), so the render and apply paths both have something to show.
func fixture(fixRan *bool) []doctor.Section {
	return []doctor.Section{{Title: "env", Results: []doctor.Result{
		{Name: "git", Status: doctor.OK, Detail: "2.50"},
		{Name: "broken", Status: doctor.Fail, Detail: "missing", Hint: "install it",
			FixLabel: "install broken", Fix: func(context.Context) error { *fixRan = true; return nil }},
	}}}
}

func TestRenderSections(t *testing.T) {
	var buf bytes.Buffer
	RenderSections(&buf, fixture(new(bool)))
	out := buf.String()
	for _, want := range []string{"env", "git", "2.50", "broken", "missing", "→ install it"} {
		if !strings.Contains(out, want) {
			t.Errorf("RenderSections output missing %q\n%s", want, out)
		}
	}
}

func TestRenderSummary(t *testing.T) {
	var ok bytes.Buffer
	RenderSummary(&ok, []doctor.Section{{Results: []doctor.Result{{Status: doctor.OK}}}})
	if !strings.Contains(ok.String(), "all good") {
		t.Errorf("clean summary = %q, want 'all good'", ok.String())
	}

	var bad bytes.Buffer
	RenderSummary(&bad, fixture(new(bool)))
	if s := bad.String(); !strings.Contains(s, "1 failing") || !strings.Contains(s, "fixable") {
		t.Errorf("summary = %q, want failing + fixable counts", s)
	}
}

func newCmd(out *bytes.Buffer) *cobra.Command {
	c := &cobra.Command{Use: "doctor"}
	c.SetOut(out)
	c.SetContext(context.Background())
	return c
}

func TestRunFixes_NonInteractiveAppliesNothing(t *testing.T) {
	var buf bytes.Buffer
	fixRan := false
	fails := RunFixes(newCmd(&buf), fixture(&fixRan), Options{Accent: brand.AccentRig, Interactive: false})
	if fixRan {
		t.Error("non-interactive without --fix must not run any fix")
	}
	if fails != 1 {
		t.Errorf("failsRemaining = %d, want 1 (fail left unfixed)", fails)
	}
	if !strings.Contains(buf.String(), "--fix") {
		t.Errorf("expected the '--fix' hint, got %q", buf.String())
	}
}

func TestRunFixes_FixAllApplies(t *testing.T) {
	var buf bytes.Buffer
	fixRan := false
	fails := RunFixes(newCmd(&buf), fixture(&fixRan), Options{Accent: brand.AccentRig, FixAll: true})
	if !fixRan {
		t.Error("--fix must run the fix")
	}
	if fails != 0 {
		t.Errorf("failsRemaining = %d, want 0 (the failing check was fixed)", fails)
	}
}
