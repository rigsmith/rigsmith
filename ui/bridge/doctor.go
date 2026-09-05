package bridge

import (
	"context"
	"errors"

	"github.com/rigsmith/rigsmith/internal/clauderig/doctor"
)

// DoctorCheck is one check as the window shows it.
//
// ID rather than Name is what the fix button sends back. Name is display text
// and rewording a label must not change what the window is allowed to ask for
// — which is the whole reason the checks carry stable ids.
type DoctorCheck struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"` // ok | warn | fail | info
	Detail string `json:"detail,omitempty"`
	// Hint is manual remediation, present only when there is no automatic fix.
	Hint string `json:"hint,omitempty"`
	// Fixable is false for a check with no repair AND for one with no id: a fix
	// this cannot name is a fix the window cannot ask for, and offering a button
	// that could only fail is worse than not offering one.
	Fixable  bool   `json:"fixable"`
	FixLabel string `json:"fixLabel,omitempty"`
}

// DoctorSection groups checks under the heading the CLI uses, so the two read
// the same way round.
type DoctorSection struct {
	Title  string        `json:"title"`
	Checks []DoctorCheck `json:"checks"`
}

// DoctorReport is a whole run.
type DoctorReport struct {
	Sections []DoctorSection `json:"sections"`
	Fails    int             `json:"fails"`
	Warns    int             `json:"warns"`
	Fixable  int             `json:"fixable"`
	// Machine and Repo say what was actually examined. A report that does not
	// name its subject is the one people misread — the window can be open while
	// the user is thinking about a different machine entirely.
	Machine string `json:"machine,omitempty"`
	Repo    string `json:"repo,omitempty"`
	Error   string `json:"error,omitempty"`
}

// Doctor runs clauderig's health checks for the window.
//
// The same doctor.Run the CLI calls, against the same doctor.NewEnv. A second
// implementation would eventually disagree with `clauderig doctor`, and a
// disagreement about whether a machine is healthy is not one anybody can
// adjudicate from the outside.
type Doctor struct{ version string }

// NewDoctor builds the doctor service. version is what the report says it ran
// as, matching the CLI's header.
func NewDoctor(version string) *Doctor { return &Doctor{version: version} }

// Get runs every check and returns the report. Local only: the checks read
// files, look for binaries on PATH and inspect the staging repo.
func (d *Doctor) Get(ctx context.Context) (DoctorReport, error) {
	env := doctor.NewEnv(ctx, d.version)
	return report(doctor.Run(ctx, env), env), nil
}

// Fix applies one check's repair and returns a freshly run report, so the pane
// shows the state after the fix rather than the state that prompted it.
//
// Errors come back inside the report rather than as a bridge error: the pane
// has somewhere to put a message, and a failed repair still wants the rest of
// the report drawn.
func (d *Doctor) Fix(ctx context.Context, id string) (DoctorReport, error) {
	env := doctor.NewEnv(ctx, d.version)
	err := doctor.Fix(ctx, env, id)
	rep := report(doctor.Run(ctx, env), env)
	switch {
	case errors.Is(err, doctor.ErrNoSuchCheck):
		// The window is out of date with the binary — the check it is naming no
		// longer exists. Say that rather than "fix failed".
		rep.Error = "that check is not in this version of clauderig"
	case errors.Is(err, doctor.ErrNotFixable):
		rep.Error = "that check has no automatic fix"
	case err != nil:
		rep.Error = err.Error()
	}
	return rep, nil
}

// report converts a run into the window's shape.
func report(sections []doctor.Section, env doctor.Env) DoctorReport {
	fails, warns, fixable := doctor.Counts(sections)
	out := DoctorReport{
		Fails: fails, Warns: warns, Fixable: fixable,
		Machine: env.Machine.Name, Repo: env.RepoName,
		Sections: make([]DoctorSection, 0, len(sections)),
	}
	for _, s := range sections {
		sec := DoctorSection{Title: s.Title, Checks: make([]DoctorCheck, 0, len(s.Results))}
		for _, r := range s.Results {
			sec.Checks = append(sec.Checks, DoctorCheck{
				ID: r.ID, Name: r.Name, Status: statusName(r.Status),
				Detail: r.Detail, Hint: r.Hint,
				Fixable: r.Fix != nil && r.ID != "", FixLabel: r.FixLabel,
			})
		}
		out.Sections = append(out.Sections, sec)
	}
	return out
}

// statusName spells the status for the frontend. A name rather than the
// underlying number, so a value added to the enum cannot silently re-colour
// every row that used to sit after it.
func statusName(s doctor.Status) string {
	switch s {
	case doctor.OK:
		return "ok"
	case doctor.Warn:
		return "warn"
	case doctor.Fail:
		return "fail"
	case doctor.Info:
		return "info"
	}
	return "info"
}
