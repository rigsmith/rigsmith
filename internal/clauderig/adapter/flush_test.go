package adapter_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rigsmith/rigsmith/internal/clauderig/adapter"
)

func TestFlushScopeKeepsSessionAndSubagentsTogether(t *testing.T) {
	root := t.TempDir()
	native := func(rel string) string { return filepath.Join(root, filepath.FromSlash(rel)) }
	scope := adapter.NewFlushScope([]string{native("projects/p/s.jsonl"), native("projects/q/t.jsonl")})
	for _, tc := range []struct {
		rel  string
		want bool
	}{
		{"projects/p/s.jsonl", true},
		{"projects/p/s/subagents/agent.jsonl", true},
		{"projects/p/s/tool-results/result.txt", true},
		{"projects/p/s-other/subagents/agent.jsonl", false},
		{"projects/p/other.jsonl", false},
		{"projects/q/t/subagents/agent.jsonl", true},
		{"projects/q/s.jsonl", false},
	} {
		if got := scope.Covers(native(tc.rel)); got != tc.want {
			t.Errorf("%s: covered=%v, want %v", tc.rel, got, tc.want)
		}
		if (adapter.FlushScope{}).Covers(native(tc.rel)) {
			t.Errorf("empty flush unexpectedly covered %s", tc.rel)
		}
	}
	// Other input names have always selected just that file, without expanding
	// a sibling directory; the caller's decoding and all-flush mode are separate.
	only := adapter.NewFlushScope([]string{native("projects/p/note.txt")})
	if !only.Covers(native("projects/p/note.txt")) || only.Covers(native("projects/p/note/child.jsonl")) {
		t.Fatal("non-transcript input expanded to a session directory")
	}
}

func TestFlushScopeResolvesNativeAliases(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real")
	if err := os.MkdirAll(filepath.Join(real, "s", "subagents"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"s.jsonl", "s/subagents/agent.jsonl"} {
		if err := os.WriteFile(filepath.Join(real, filepath.FromSlash(rel)), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(real, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	scope := adapter.NewFlushScope([]string{filepath.Join(alias, "s.jsonl")})
	if !scope.Covers(filepath.Join(real, "s.jsonl")) || !scope.Covers(filepath.Join(real, "s", "subagents", "agent.jsonl")) {
		t.Fatal("hook alias and walked source did not resolve to the same session")
	}
}
