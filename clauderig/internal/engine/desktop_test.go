package engine

import (
	"path/filepath"
	"testing"

	"github.com/rigsmith/clauderig/internal/config"
	"github.com/rigsmith/core/pathmap"
)

func twoRootConfig(cliDir, deskDir string) *config.Config {
	c := config.Default()
	c.Roots = []config.Root{
		{ID: "cli", Enabled: true, Location: pathmap.Cascade{Portable: cliDir}},
		{ID: "desktop", Enabled: true, Location: pathmap.Cascade{Portable: deskDir}},
	}
	return c
}

// A Desktop session file's cwd must portablize on sync and resolve to the target
// machine on restore — the Q4 value-based rewrite, end to end through the engine.
func TestDesktopValueRewrite_RoundTrip(t *testing.T) {
	liveCli, liveDesk := t.TempDir(), t.TempDir()
	write(t, liveDesk, "claude-code-sessions/uuid/local_1.json",
		`{"cwd":"/Users/john/Git/proj","originCwd":"/Users/john/Git","model":"fable","other":"/tmp"}`)

	staging := t.TempDir()
	john := config.Machine{Name: "john", OS: pathmap.OSMacOS, Home: "/Users/john"}
	if _, err := Sync(Options{StagingDir: staging, Config: twoRootConfig(liveCli, liveDesk), Machine: john}); err != nil {
		t.Fatal(err)
	}
	staged := read(t, filepath.Join(staging, "desktop", "claude-code-sessions", "uuid", "local_1.json"))
	if !contains(staged, "$HOME/Git/proj") || contains(staged, "/Users/john") {
		t.Fatalf("desktop cwd not portablized: %s", staged)
	}

	targetCli, targetDesk := t.TempDir(), t.TempDir()
	jane := config.Machine{Name: "jane", OS: pathmap.OSMacOS, Home: "/Users/jane"}
	if _, err := Restore(RestoreOptions{StagingDir: staging, Config: twoRootConfig(targetCli, targetDesk), Machine: jane}); err != nil {
		t.Fatal(err)
	}
	restored := read(t, filepath.Join(targetDesk, "claude-code-sessions", "uuid", "local_1.json"))
	if !contains(restored, "/Users/jane/Git/proj") {
		t.Errorf("cwd not resolved to jane: %s", restored)
	}
	if !contains(restored, `"other": "/tmp"`) {
		t.Errorf("/tmp should be untouched: %s", restored)
	}
	if !contains(restored, `"model": "fable"`) {
		t.Errorf("non-path value changed: %s", restored)
	}
}
