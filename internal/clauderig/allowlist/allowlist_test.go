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

func TestNodeModules_PrunedAtAnyDepth(t *testing.T) {
	// A Cowork session that ran a build, and a skill with npm deps: the vendored
	// tree is excluded however deep it sits, and the exclude outranks the (much
	// longer) include pattern it sits inside.
	deep := "local-agent-mode-sessions/03d/e30/local_x/outputs/build/node_modules/xml-js/package.json"
	if Desktop().Match(deep) {
		t.Error("node_modules under a session should not sync")
	}
	if CLI().Match("skills/my-skill/node_modules/left-pad/index.js") {
		t.Error("node_modules under a skill should not sync")
	}
	// Siblings of the vendored tree still sync — the prune is name-scoped, not a
	// blanket exclude of everything below outputs/.
	if !Desktop().Match("local-agent-mode-sessions/03d/e30/local_x/outputs/build/package.json") {
		t.Error("lockfile beside node_modules should still sync")
	}
	// And the walk never enters it.
	if Desktop().descend("local-agent-mode-sessions/03d/e30/local_x/outputs/build/node_modules") {
		t.Error("should prune the node_modules dir, not descend it")
	}
}

func TestDesktop_PrunesCacheTree(t *testing.T) {
	// Build a Desktop-like tree: a giant junk dir + the allowed small files.
	root := t.TempDir()
	mustWrite(t, root, "Cache/data_0", "junk")
	mustWrite(t, root, "GPUCache/x", "junk")
	mustWrite(t, root, "IndexedDB/y", "junk")
	mustWrite(t, root, "config.json", "{}")                // synced (keep-filtered to preferences)
	mustWrite(t, root, "claude_desktop_config.json", "{}") // synced (MCP config)
	mustWrite(t, root, "claude-code-sessions/03d/uuid/local_1.json", "{}")
	mustWrite(t, root, "window-state.json", "{}") // machine-local, excluded

	got, _, err := Walk(root, Desktop())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"claude-code-sessions/03d/uuid/local_1.json",
		"claude_desktop_config.json",
		"config.json",
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

	got, _, err := Walk(root, CLI())
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

func TestWalk_SymlinkedMemoryDirReportedAsLink(t *testing.T) {
	// A worktree project slug shares memory with its main project via a symlink
	// (memory -> ../<main-slug>/memory). The link must not surface as a file —
	// that made sync read a directory and abort — but is reported as a Link so
	// restore can recreate it; the content syncs under its canonical slug.
	root := t.TempDir()
	mustWrite(t, root, "projects/-main/memory/MEMORY.md", "x")
	mustWrite(t, root, "projects/-main-worktree/s.jsonl", "{}")
	link := filepath.Join(root, "projects", "-main-worktree", "memory")
	if err := os.Symlink(filepath.Join(root, "projects", "-main", "memory"), link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	// A directory link sitting at an excluded path is dropped, not reported.
	if err := os.Symlink(filepath.Join(root, "projects", "-main", "memory"), filepath.Join(root, "statsig")); err != nil {
		t.Fatal(err)
	}

	got, links, err := Walk(root, CLI())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"projects/-main-worktree/s.jsonl",
		"projects/-main/memory/MEMORY.md",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Walk = %v, want %v", got, want)
	}
	wantLinks := []Link{{Rel: "projects/-main-worktree/memory", Target: "projects/-main/memory"}}
	if !reflect.DeepEqual(links, wantLinks) {
		t.Fatalf("links = %v, want %v", links, wantLinks)
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
