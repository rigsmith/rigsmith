package allowlist

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestCLI_Match(t *testing.T) {
	l := CLI()
	in := []string{
		"settings.json", "settings.local.json", "CLAUDE.md",
		"skills/use-railway/SKILL.md", "plans/x.md",
		"plugins/marketplaces/gitkraken/m.json", "plugins/data/x",
		"projects/-Users-john-Git-rigsmith/s.jsonl",
	}
	out := []string{
		"history.jsonl", "stats-cache.json", "statsig/x",
		"sessions/10010.json", "shell-snapshots/s.sh",
		"plugins/cache/blob", "ide/123.lock",
		".credentials.json", "projects/-x/file-history/a",
	}
	for _, p := range in {
		if !l.Match(p) {
			t.Errorf("want INCLUDE %q", p)
		}
	}
	for _, p := range out {
		if l.Match(p) {
			t.Errorf("want EXCLUDE %q", p)
		}
	}
}

func TestLongestWins_CarveOut(t *testing.T) {
	// projects/ included, but the file-history carve-out (longer) wins.
	l := CLI()
	if !l.Match("projects/-x/s.jsonl") {
		t.Error("transcript should sync")
	}
	if l.Match("projects/-x/file-history/snap") {
		t.Error("file-history carve-out should win over projects include")
	}
}

func TestDesktop_PrunesCacheTree(t *testing.T) {
	// Build a Desktop-like tree: a giant junk dir + the allowed small files.
	root := t.TempDir()
	mustWrite(t, root, "Cache/data_0", "junk")
	mustWrite(t, root, "GPUCache/x", "junk")
	mustWrite(t, root, "IndexedDB/y", "junk")
	mustWrite(t, root, "config.json", "{}")                // volatile — excluded
	mustWrite(t, root, "claude_desktop_config.json", "{}") // synced (MCP config)
	mustWrite(t, root, "claude-code-sessions/03d/uuid/local_1.json", "{}")
	mustWrite(t, root, "window-state.json", "{}") // machine-local, excluded

	got, err := Walk(root, Desktop())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"claude-code-sessions/03d/uuid/local_1.json",
		"claude_desktop_config.json",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Walk = %v, want %v", got, want)
	}
}

func TestDescend_Pruning(t *testing.T) {
	l := Desktop()
	// must descend into an allowed tree and its parent-of-include
	if !l.descend("claude-code-sessions") || !l.descend("claude-code-sessions/03d") {
		t.Error("should descend allowed session tree")
	}
	// must NOT descend cache junk
	if l.descend("Cache") || l.descend("GPUCache") || l.descend("blob_storage") {
		t.Error("should prune cache dirs")
	}
}

func TestCLI_Walk(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, root, "settings.json", "{}")
	mustWrite(t, root, "skills/a/SKILL.md", "x")
	mustWrite(t, root, "projects/-p/s.jsonl", "{}")
	mustWrite(t, root, "plugins/marketplaces/m.json", "{}")
	mustWrite(t, root, "plugins/cache/blob", "junk")
	mustWrite(t, root, "statsig/s", "junk")
	mustWrite(t, root, "history.jsonl", "junk")

	got, err := Walk(root, CLI())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"plugins/marketplaces/m.json",
		"projects/-p/s.jsonl",
		"settings.json",
		"skills/a/SKILL.md",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Walk = %v, want %v", got, want)
	}
}

func mustWrite(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
