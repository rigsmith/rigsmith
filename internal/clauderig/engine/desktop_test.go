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

// The Desktop config.json is reduced to the keys that are stable and portable —
// the volatile caches/tokens (which previously tripped the wire) are dropped
// before sync. The fixture mirrors the document Desktop actually writes: flat,
// colon-namespaced keys, NOT the nested objects an earlier fixture assumed.
func TestSync_DesktopConfigKeepFilter(t *testing.T) {
	liveCli, liveDesk := t.TempDir(), t.TempDir()
	write(t, liveDesk, "config.json",
		`{"locale":"en-US","userThemeMode":"dark","updaterLastSeenVersion":"1.2.3",`+
			`"lastKnownAccountUuid":"03d1c0c9-823d-464b-a468-a9bea2383338",`+
			`"oauth:tokenCache":"Zk9q3xR7tLmA1cD8eF0gH2iJ4kL6mN8oP0qR2sT4uV6wX8y",`+
			`"oauth:tokenCacheV2":"Bb2Cc3Dd4Ee5Ff6Gg7Hh8Ii9Jj0Kk1Ll2Mm3Nn4Oo5Pp6Qq7",`+
			`"dxt:allowlistCache:sid":{"x":"Aa1Bb2Cc3Dd4Ee5Ff6Gg7Hh8Ii9Jj0Kk1Ll2Mm3Nn4"}}`)

	staging := t.TempDir()
	john := config.Machine{Name: "john", OS: pathmap.OSMacOS, Home: "/Users/john"}
	rep, err := Sync(Options{StagingDir: staging, Config: twoRootConfig(liveCli, liveDesk), Machine: john, SourceOverride: override("cli", liveCli, "desktop", liveDesk)})
	if err != nil {
		t.Fatalf("sync: %v (findings=%v)", err, rep.Findings)
	}
	staged := read(t, filepath.Join(staging, "desktop", "config.json"))
	// The portable preferences survive — the whole point of syncing this file.
	for _, kept := range []string{"locale", "en-US", "userThemeMode", "dark"} {
		if !contains(staged, kept) {
			t.Errorf("portable key %q should have been kept: %s", kept, staged)
		}
	}
	// Secrets, caches, identity and machine state are all dropped.
	for _, gone := range []string{
		"tokenCache", "tokenCacheV2", "allowlistCache",
		"lastKnownAccountUuid", "updaterLastSeenVersion",
	} {
		if contains(staged, gone) {
			t.Errorf("volatile key %q should have been dropped: %s", gone, staged)
		}
	}
}

// A `preferences` object is kept too, so the filter still works if Desktop moves
// its settings back under one — the reason the key stays in the list.
func TestSync_DesktopConfigKeepsPreferencesObject(t *testing.T) {
	liveCli, liveDesk := t.TempDir(), t.TempDir()
	write(t, liveDesk, "config.json",
		`{"preferences":{"sidebarMode":"compact"},"first_launch_at":1750000000}`)

	staging := t.TempDir()
	john := config.Machine{Name: "john", OS: pathmap.OSMacOS, Home: "/Users/john"}
	if _, err := Sync(Options{StagingDir: staging, Config: twoRootConfig(liveCli, liveDesk), Machine: john, SourceOverride: override("cli", liveCli, "desktop", liveDesk)}); err != nil {
		t.Fatalf("sync: %v", err)
	}
	staged := read(t, filepath.Join(staging, "desktop", "config.json"))
	if !contains(staged, "sidebarMode") {
		t.Errorf("preferences should be kept: %s", staged)
	}
	if contains(staged, "first_launch_at") {
		t.Errorf("machine state should have been dropped: %s", staged)
	}
}

// Restore must count each Desktop Code-session sidecar it writes — the number
// behind the restart nudge — and only those: a non-sidecar json, a local_*
// directory, and a same-named file under the CLI root must not inflate it.
func TestRestore_CountsDesktopSidecars(t *testing.T) {
	staging := t.TempDir()
	deskStage := filepath.Join(staging, "desktop")
	write(t, deskStage, "claude-code-sessions/uuid/local_a.json", `{"cliSessionId":"a"}`)
	write(t, deskStage, "claude-code-sessions/uuid/local_b.json", `{"cliSessionId":"b"}`)
	write(t, deskStage, "claude-code-sessions/uuid/other.json", `{"x":1}`)             // not a sidecar
	write(t, deskStage, "claude-code-sessions/uuid/local_cache/inner.json", `{"y":2}`) // local_ is a DIR
	write(t, filepath.Join(staging, "cli"), "projects/-slug/local_z.json", `{"z":3}`)  // CLI root, must not count

	targetCli, targetDesk := t.TempDir(), t.TempDir()
	jane := config.Machine{Name: "jane", OS: pathmap.OSMacOS, Home: "/Users/jane"}
	rep, err := Restore(RestoreOptions{
		StagingDir: staging, Config: twoRootConfig(targetCli, targetDesk),
		Machine: jane, TargetOverride: override("cli", targetCli, "desktop", targetDesk),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := rep.DesktopSessions(); got != 2 {
		t.Errorf("DesktopSessions() = %d, want 2 (only the two local_*.json sidecars)", got)
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
