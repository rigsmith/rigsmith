package bridge

import (
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/clauderig/devices"
	"github.com/rigsmith/rigsmith/internal/clauderig/sessions"
)

func TestLibraryScope_Defaults(t *testing.T) {
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)

	// An unconfigured window gets a window, not everything on the disk: with no
	// lower bound every transcript is opened to be dated.
	sc, err := libraryScope("", "mbp", now)
	if err != nil {
		t.Fatal(err)
	}
	if want := now.AddDate(0, 0, -30); !sc.Since.Equal(want) {
		t.Errorf("default Since = %s, want %s (%s)", sc.Since, want, DefaultLibrarySince)
	}

	// "all" is the deliberate escape hatch, and must leave NO lower bound
	// rather than a very old one.
	if sc, err := libraryScope("all", "mbp", now); err != nil || !sc.Since.IsZero() {
		t.Errorf("since=all gave %v %v, want the zero time", sc.Since, err)
	}
	if sc, err := libraryScope("ALL", "mbp", now); err != nil || !sc.Since.IsZero() {
		t.Error("since=all should be case-insensitive")
	}

	// A bad value is reported, not silently swallowed into "everything".
	if _, err := libraryScope("last tuesday", "mbp", now); err == nil {
		t.Error("an unreadable since should error rather than widen the window")
	}
}

func TestLibraryView_MapsRowsAndCoverage(t *testing.T) {
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	rows := []sessions.Row{{
		ID: "abcdef01-2345-4678-89ab-cdef01234567", When: now.Add(-time.Hour),
		Cwd: "/work/api", Account: "acct-uuid", AccountLabel: "john@example.com",
		Title: "Database work", LastPrompt: "now add the migration",
		Client: "cli@work", Branch: "feat/x", Sources: []string{"cli", "repo"},
		CLILive: true, InRepo: true, Present: true, Profile: "work",
	}}
	rep := sessions.Report{Read: 12, Skipped: 300, Total: 1, Undated: 2, Unattributed: 3, Approx: 1}
	scope := sessions.Scope{
		Now: now, Me: "mbp", LiveInScope: true,
		Devices: []devices.Device{
			{Name: "mbp", LastSync: now.Add(-time.Minute)},
			{Name: "air", LastSync: now.Add(-8 * 24 * time.Hour)},
		},
	}

	v := libraryView(rows, rep, "mbp", scope)

	if len(v.Sessions) != 1 {
		t.Fatalf("got %d sessions, want 1", len(v.Sessions))
	}
	s := v.Sessions[0]
	if s.Short != "abcdef01" {
		t.Errorf("Short = %q, want the first id segment", s.Short)
	}
	if s.Title != "Database work" || s.LastPrompt != "now add the migration" {
		t.Errorf("title/last prompt = %q/%q", s.Title, s.LastPrompt)
	}
	// Title and last prompt are separate columns on purpose — what a session was
	// opened to do and what it was last asked to do are rarely the same.
	if s.Title == s.LastPrompt {
		t.Error("title and last prompt collapsed into one value")
	}
	if s.AccountLabel != "john@example.com" || s.Account != "acct-uuid" {
		t.Errorf("account = %q/%q, want both the label and the uuid", s.AccountLabel, s.Account)
	}
	if len(s.Sources) != 2 {
		t.Errorf("Sources = %v, want every store named", s.Sources)
	}

	if v.Total != 1 || v.Read != 12 || v.Skipped != 300 {
		t.Errorf("counters = %d/%d/%d", v.Total, v.Read, v.Skipped)
	}
	// The excluded-for-lacking-information counts must survive into the view;
	// a list that shrinks silently reads as "there is nothing there".
	if v.Undated != 2 || v.Unattributed != 3 || v.Approx != 1 {
		t.Errorf("undated/unattributed/approx = %d/%d/%d", v.Undated, v.Unattributed, v.Approx)
	}
	// This machine was read live, so its own sync age hides nothing; the other
	// one is a week stale and its sessions are genuinely not covered.
	if len(v.Stale) != 1 || v.Stale[0] != "air" {
		t.Errorf("Stale = %v, want only the stale OTHER machine", v.Stale)
	}
}

// No devices at all is "nothing to say"; an unreadable registry is "coverage
// could not be established". The window must be able to tell them apart.
func TestLibraryView_UnavailableRegistryIsNotSilence(t *testing.T) {
	v := libraryView(nil, sessions.Report{}, "mbp", sessions.Scope{DevicesUnavailable: true})
	if !v.DevicesUnavailable {
		t.Error("an unreadable device registry was reported as simply having no devices")
	}
}
