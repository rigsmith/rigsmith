package engine

import (
	"path/filepath"
	"testing"

	"github.com/rigsmith/rigsmith/core/pathmap"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
)

func twoRootConfig(cliDir, deskDir string) *config.Config {
	c := config.Default()
	c.Roots = []config.Root{
		{ID: "cli", Enabled: true, Location: pathmap.Cascade{Portable: cliDir}},
		{ID: "desktop", Enabled: true, Location: pathmap.Cascade{Portable: deskDir}},
	}
	return c
}

// The Desktop config.json is reduced to its stable `preferences` — the volatile
// caches/tokens (which previously tripped the wire) are dropped before sync.
func TestSync_DesktopConfigKeepFilter(t *testing.T) {
	liveCli, liveDesk := t.TempDir(), t.TempDir()
	write(t, liveDesk, "config.json",
		`{"preferences":{"sidebarMode":"compact","coworkWebSearchEnabled":true},`+
			`"oauth":{"tokenCache":"Zk9q3xR7tLmA1cD8eF0gH2iJ4kL6mN8oP0qR2sT4uV6wX8y"},`+
			`"dxt":{"allowlistCache":{"sid":"Aa1Bb2Cc3Dd4Ee5Ff6Gg7Hh8Ii9Jj0Kk1Ll2Mm3Nn4"}}}`)

	staging := t.TempDir()
	john := config.Machine{Name: "john", OS: pathmap.OSMacOS, Home: "/Users/john"}
	rep, err := Sync(Options{StagingDir: staging, Config: twoRootConfig(liveCli, liveDesk), Machine: john, SourceOverride: override("cli", liveCli, "desktop", liveDesk)})
	if err != nil {
		t.Fatalf("sync: %v (findings=%v)", err, rep.Findings)
	}
	staged := read(t, filepath.Join(staging, "desktop", "config.json"))
	if !contains(staged, "sidebarMode") {
		t.Errorf("preferences should be kept: %s", staged)
	}
	for _, gone := range []string{"oauth", "tokenCache", "dxt", "allowlistCache"} {
		if contains(staged, gone) {
			t.Errorf("volatile key %q should have been dropped: %s", gone, staged)
		}
	}
}

// A Desktop session file's cwd must portablize on sync and resolve to the target
// machine on restore — the Q4 value-based rewrite, end to end through the engine.
func TestDesktopValueRewrite_RoundTrip(t *testing.T) {
	liveCli, liveDesk := t.TempDir(), t.TempDir()
	write(t, liveDesk, "claude-code-sessions/uuid/local_1.json",
		`{"cwd":"/Users/john/Git/proj","originCwd":"/Users/john/Git","model":"fable","other":"/tmp"}`)

	staging := t.TempDir()
	john := config.Machine{Name: "john", OS: pathmap.OSMacOS, Home: "/Users/john"}
	if _, err := Sync(Options{StagingDir: staging, Config: twoRootConfig(liveCli, liveDesk), Machine: john, SourceOverride: override("cli", liveCli, "desktop", liveDesk)}); err != nil {
		t.Fatal(err)
	}
	staged := read(t, filepath.Join(staging, "desktop", "claude-code-sessions", "uuid", "local_1.json"))
	if !contains(staged, "$HOME/Git/proj") || contains(staged, "/Users/john") {
		t.Fatalf("desktop cwd not portablized: %s", staged)
	}

	targetCli, targetDesk := t.TempDir(), t.TempDir()
	jane := config.Machine{Name: "jane", OS: pathmap.OSMacOS, Home: "/Users/jane"}
	if _, err := Restore(RestoreOptions{StagingDir: staging, Config: twoRootConfig(targetCli, targetDesk), Machine: jane, TargetOverride: override("cli", targetCli, "desktop", targetDesk)}); err != nil {
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
