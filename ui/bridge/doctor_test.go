package bridge

import (
	"context"
	"testing"

	"github.com/rigsmith/rigsmith/internal/clauderig/doctor"
)

// The window offers a fix button per check, and the button sends an id. A check
// that reports a problem it can repair but carries no id cannot be asked for,
// so the button would be one that could only fail — Fixable has to say false.
func TestReport_AFixWithNoIDIsNotOffered(t *testing.T) {
	fix := func(context.Context) error { return nil }
	sections := []doctor.Section{{Title: "hooks", Results: []doctor.Result{
		{ID: "hooks-installed", Name: "sync hooks", Status: doctor.Fail, Fix: fix, FixLabel: "install them"},
		{Name: "nameless", Status: doctor.Warn, Fix: fix},
		{ID: "reports-only", Name: "disk", Status: doctor.Warn, Hint: "free some space"},
	}}}

	got := report(sections, doctor.Env{})
	if len(got.Sections) != 1 || len(got.Sections[0].Checks) != 3 {
		t.Fatalf("shape: %+v", got)
	}
	checks := got.Sections[0].Checks
	if !checks[0].Fixable || checks[0].FixLabel != "install them" {
		t.Errorf("a fixable check with an id was not offered: %+v", checks[0])
	}
	if checks[1].Fixable {
		t.Error("a fix with no id was offered, and nothing could act on it")
	}
	if checks[2].Fixable || checks[2].Hint == "" {
		t.Errorf("a report-only check should carry its hint and no button: %+v", checks[2])
	}
}

// Status crosses the bridge as a name. The underlying type is an int, and
// sending the number would let a value inserted into the enum silently
// re-colour every row that used to sit after it.
func TestStatusName_SpellsEveryStatus(t *testing.T) {
	for _, tc := range []struct {
		in   doctor.Status
		want string
	}{
		{doctor.OK, "ok"}, {doctor.Warn, "warn"}, {doctor.Fail, "fail"}, {doctor.Info, "info"},
	} {
		if got := statusName(tc.in); got != tc.want {
			t.Errorf("statusName(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
	// An unknown status must not read as a problem: "info" is the neutral one.
	if got := statusName(doctor.Status(99)); got != "info" {
		t.Errorf("an unknown status came back as %q, want the neutral one", got)
	}
}

// The counts drive the pane's summary line, and they come from the same
// doctor.Counts the CLI prints.
func TestReport_CarriesTheCounts(t *testing.T) {
	fix := func(context.Context) error { return nil }
	sections := []doctor.Section{{Title: "sync", Results: []doctor.Result{
		{ID: "a", Status: doctor.Fail, Fix: fix},
		{ID: "b", Status: doctor.Warn},
		{ID: "c", Status: doctor.OK},
		{ID: "d", Status: doctor.Info},
	}}}
	got := report(sections, doctor.Env{})
	if got.Fails != 1 || got.Warns != 1 || got.Fixable != 1 {
		t.Errorf("counts = fails %d warns %d fixable %d, want 1/1/1", got.Fails, got.Warns, got.Fixable)
	}
}

// A report has to name what it examined. The window can sit open while someone
// is thinking about a different machine.
func TestReport_NamesItsSubject(t *testing.T) {
	env := doctor.Env{RepoName: "rigsmith"}
	env.Machine.Name = "Pro16"
	got := report(nil, env)
	if got.Machine != "Pro16" || got.Repo != "rigsmith" {
		t.Errorf("report does not name its subject: %+v", got)
	}
}

// The window has no repository, so the repo-scoped checks do not run and leave
// a placeholder telling a terminal user to go and stand in one. That advice
// cannot be followed in a window, so it is noise there — and a section left
// holding nothing but the placeholder should not appear at all.
func TestReport_DropsTheNoRepoPlaceholder(t *testing.T) {
	sections := []doctor.Section{
		{Title: "worktree discipline", Results: []doctor.Result{
			{ID: "global-hooks", Name: "global sync hooks", Status: doctor.Warn, Detail: "partial"},
			{ID: repoPlaceholder, Name: "repo checks", Status: doctor.Info, Detail: "not in a git repo"},
		}},
		{Title: "nothing but advice", Results: []doctor.Result{
			{ID: repoPlaceholder, Status: doctor.Info},
		}},
	}

	got := report(sections, doctor.Env{})
	if len(got.Sections) != 1 {
		t.Fatalf("got %d sections, want the empty one dropped: %+v", len(got.Sections), got.Sections)
	}
	if n := len(got.Sections[0].Checks); n != 1 {
		t.Fatalf("got %d checks, want the placeholder dropped", n)
	}
	if got.Sections[0].Checks[0].ID != "global-hooks" {
		t.Errorf("kept the wrong check: %+v", got.Sections[0].Checks[0])
	}
}
