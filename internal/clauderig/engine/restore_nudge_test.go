package engine

import "testing"

func TestIsDesktopSessionSidecar(t *testing.T) {
	yes := []string{
		"claude-code-sessions/org/user/local_sess-aaa.json",
		"claude-code-sessions/o/u/local_abc-123.json",
	}
	no := []string{
		"claude-code-sessions/org/user/other.json",  // not a local_ sidecar
		"claude-code-sessions/org/user/local_x.txt", // not json
		"claude_desktop_config.json",                // wrong tree
		"projects/-slug/local_x.json",               // right name, wrong tree
	}
	for _, r := range yes {
		if !isDesktopSessionSidecar(r) {
			t.Errorf("isDesktopSessionSidecar(%q) = false, want true", r)
		}
	}
	for _, r := range no {
		if isDesktopSessionSidecar(r) {
			t.Errorf("isDesktopSessionSidecar(%q) = true, want false", r)
		}
	}
}

func TestReportDesktopSessionsSum(t *testing.T) {
	rep := &RestoreReport{Roots: []RestoreRootResult{
		{ID: "cli", DesktopSessions: 0},
		{ID: "desktop", DesktopSessions: 3},
	}}
	if got := rep.DesktopSessions(); got != 3 {
		t.Errorf("DesktopSessions() = %d, want 3", got)
	}
}
