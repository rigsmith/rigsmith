package bridge

import (
	"context"

	"github.com/rigsmith/rigsmith/internal/clauderig/desktop"
)

// PanelProfile is one Claude Desktop profile as the tray's popover lists it.
type PanelProfile struct {
	Name  string `json:"name"`
	Email string `json:"email,omitempty"`
	// Open is whether this profile has a window up right now. It decides what
	// one click does — focus what is there, or start what is not — which is why
	// the popover is worth a live read rather than a cached list.
	Open bool `json:"open"`
	// PID is that window's main process, and it is what makes the two halves of
	// this UI behave identically: the tray menu raises a pid, so the popover
	// raises the same pid rather than asking the CLI to work out which window
	// was meant. Zero when the profile is closed, which is the other path.
	PID int `json:"pid,omitempty"`
}

// PanelView is everything the popover draws: the profiles, the machine-wide
// app, and one line of sync health.
//
// One call rather than three. The popover is opened from a menu bar click and
// has to be complete before it is on screen; three round trips would draw it in
// stages, which reads as a window still making its mind up.
type PanelView struct {
	Profiles []PanelProfile `json:"profiles"`
	// MainOpen reports the machine-wide install — the one with no profile. It
	// is listed apart from the profiles because it is not one: no account is
	// bound to it, and clauderig cannot name it.
	MainOpen bool `json:"mainOpen"`
	// MainPID is that window's process, for the same reason every profile row
	// carries one: an open window is raised in-process, by pid, the way the
	// tray menu raises it. Only a window that does not exist needs the CLI.
	MainPID int `json:"mainPid,omitempty"`
	// Installed is false when Claude Desktop is not on this machine at all, so
	// the popover can say that rather than showing an empty list that looks
	// like a failure.
	Installed bool `json:"installed"`
	// Level and Summary are the tray icon's own colour and sentence, repeated
	// here so the popover can carry the status it replaced as the first click.
	Level   string `json:"level"`
	Summary string `json:"summary"`
	// Error is a process scan that failed. The profiles are still listed — the
	// store knows them — but whether they are open is unknown, and saying
	// "closed" would send a click to launch a second window.
	Error string `json:"error,omitempty"`
}

// Panel reads everything the popover needs.
func (d *Desktop) Panel(ctx context.Context) (PanelView, error) {
	var v PanelView
	_, v.Installed = d.app.Installed()

	if rep, err := NewStatus().Health(ctx); err == nil {
		v.Level, v.Summary = rep.Level.String(), rep.Summary
	} else {
		// A status we cannot read is amber, the same answer the tray gives.
		v.Level, v.Summary = "amber", "status unavailable"
	}

	st, err := desktop.DefaultStore()
	if err != nil {
		v.Error = err.Error()
		return v, nil
	}
	profiles, lerr := st.List()
	if lerr != nil {
		v.Error = lerr.Error()
		return v, nil
	}

	// One scan for every profile, rather than one per profile: the popover is
	// on the click path, and a process scan per profile is the difference
	// between a window that appears and a window that arrives.
	// Instances, not one Running() per profile: Running matches the profile flag
	// anywhere in a command line and every Electron helper inherits it, so it
	// answers with a dozen processes that have no windows. The pid recorded here
	// has to be one that can actually be raised.
	pid := map[string]int{}
	instances, ierr := d.app.Instances()
	if ierr != nil {
		v.Error = ierr.Error()
	}
	for _, inst := range instances {
		if inst.DataDir == "" {
			v.MainOpen, v.MainPID = true, inst.PID
			continue
		}
		pid[desktop.CanonicalDir(inst.DataDir)] = inst.PID
	}

	for _, p := range profiles {
		row := PanelProfile{Name: p.Name, Email: p.Email}
		if got, ok := pid[desktop.CanonicalDir(p.DataDir())]; ok {
			row.Open, row.PID = true, got
		}
		v.Profiles = append(v.Profiles, row)
	}
	return v, nil
}
