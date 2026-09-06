package adapter_test

import (
	"testing"

	"github.com/rigsmith/rigsmith/internal/clauderig/adapter"
)

func TestIsDesktopSessionSidecar(t *testing.T) {
	yes := []string{
		"claude-code-sessions/org/user/local_sess-aaa.json",
		"claude-code-sessions/o/u/local_abc-123.json",
	}
	no := []string{
		"claude-code-sessions/org/user/other.json",             // not a local_ sidecar
		"claude-code-sessions/org/user/local_x.txt",            // not json
		"claude_desktop_config.json",                           // wrong tree
		"projects/-slug/local_x.json",                          // right name, wrong tree
		"claude-code-sessions/org/local_cache/not-a-sess.json", // local_ is a DIR, not the file
	}
	for _, r := range yes {
		if !adapter.IsDesktopCodeSidecar(r) {
			t.Errorf("adapter.IsDesktopCodeSidecar(%q) = false, want true", r)
		}
	}
	for _, r := range no {
		if adapter.IsDesktopCodeSidecar(r) {
			t.Errorf("adapter.IsDesktopCodeSidecar(%q) = true, want false", r)
		}
	}
}
