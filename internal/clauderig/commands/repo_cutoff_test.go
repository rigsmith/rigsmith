package commands

import (
	"testing"
	"time"
)

// An age is a duration from right now, so "7d" cut at whatever o'clock it
// happened to be — which is how a repo ended up reporting that its history began
// at 08:18 on a Tuesday. The cut is snapped to local midnight so "before this
// date" means what it says.
func TestPruneCutoff_SnapsAgesToADayBoundary(t *testing.T) {
	now := time.Date(2026, 8, 28, 15, 47, 31, 0, time.Local)
	got, err := pruneCutoff("7d", now)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 8, 21, 0, 0, 0, 0, time.Local)
	if !got.Equal(want) {
		t.Errorf("pruneCutoff(7d) = %v, want %v", got, want)
	}
}

// A bare date is already a boundary and is taken as given, so --before 2026-08-01
// keeps all of August.
func TestPruneCutoff_AcceptsADate(t *testing.T) {
	got, err := pruneCutoff("2026-08-01", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 8, 1, 0, 0, 0, 0, time.Local)
	if !got.Equal(want) {
		t.Errorf("pruneCutoff(date) = %v, want %v", got, want)
	}
}

func TestPruneCutoff_RejectsNonsense(t *testing.T) {
	for _, spec := range []string{"", "   ", "0d", "-3d", "tuesday"} {
		if _, err := pruneCutoff(spec, time.Now()); err == nil {
			t.Errorf("pruneCutoff(%q) was accepted", spec)
		}
	}
}

func TestHumanSpan_AnswersTheQuestionAsAsked(t *testing.T) {
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Minute, "under an hour"},
		{28 * time.Hour, "28 hours"},
		{7 * 24 * time.Hour, "7 days"},
		{90 * 24 * time.Hour, "3 months"},
	} {
		if got := humanSpan(tc.d); got != tc.want {
			t.Errorf("humanSpan(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}
