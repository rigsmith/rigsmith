package engine

import "testing"

func TestReportDesktopSessionsSum(t *testing.T) {
	rep := &RestoreReport{Roots: []RestoreRootResult{
		{ID: "cli", DesktopSessions: 0},
		{ID: "desktop", DesktopSessions: 3},
	}}
	if got := rep.DesktopSessions(); got != 3 {
		t.Errorf("DesktopSessions() = %d, want 3", got)
	}
}
