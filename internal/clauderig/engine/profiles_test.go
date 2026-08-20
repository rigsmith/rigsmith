package engine

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/rigsmith/rigsmith/core/pathmap"
	"github.com/rigsmith/rigsmith/internal/clauderig/allowlist"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
)

// desktopProfileConfig points the desktop root at dir; profiles inherit its
// enabled flag.
func desktopProfileConfig(dir string, enabled bool) *config.Config {
	c := config.Default()
	c.Roots = []config.Root{{ID: "desktop", Enabled: enabled, Location: pathmap.Cascade{Portable: dir}}}
	return c
}

func TestSync_StagesEachProfileUnderItsOwnRoot(t *testing.T) {
	work, personal := t.TempDir(), t.TempDir()
	write(t, work, "profile.json", `{"name":"work","email":"a@example.com"}`)
	write(t, work, "data/claude-code-sessions/acct/org/local_a.json", `{"cliSessionId":"s1"}`)
	write(t, personal, "data/claude-code-sessions/acct/org/local_b.json", `{"cliSessionId":"s2"}`)
	// Present in both, and in neither allowlist: a profile must not widen what
	// syncs just by being walked as a root of its own.
	write(t, work, "data/Cookies", "sqlite goes here")
	write(t, personal, "data/Local Storage/leveldb/000003.log", "session tokens")

	staging := t.TempDir()
	m := config.Machine{Name: "mbp", OS: pathmap.OSMacOS, Home: "/Users/john"}
	rep, err := Sync(Options{
		StagingDir: staging, Config: desktopProfileConfig(t.TempDir(), true), Machine: m,
		Profiles:       []string{"work", "personal"},
		SourceOverride: override("desktop@work", work, "desktop@personal", personal),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ root, rel string }{
		// profile.json travels too, or a restored machine would have the files
		// without an entry `clauderig desktop open` can find.
		{"desktop@work", "profile.json"},
		{"desktop@work", "data/claude-code-sessions/acct/org/local_a.json"},
		{"desktop@personal", "data/claude-code-sessions/acct/org/local_b.json"},
	} {
		if _, err := os.Stat(filepath.Join(staging, tc.root, filepath.FromSlash(tc.rel))); err != nil {
			t.Errorf("%s/%s not staged: %v", tc.root, tc.rel, err)
		}
	}
	for _, denied := range []string{
		filepath.Join(staging, "desktop@work", "data", "Cookies"),
		filepath.Join(staging, "desktop@personal", "data", "Local Storage", "leveldb", "000003.log"),
	} {
		if _, err := os.Stat(denied); err == nil {
			t.Errorf("%s synced — the Desktop allowlist must govern profiles too", denied)
		}
	}
	var ids []string
	for _, r := range rep.Roots {
		ids = append(ids, r.ID)
	}
	if len(ids) != 3 {
		t.Fatalf("roots reported = %v, want desktop plus one per profile", ids)
	}
}

func TestSync_ProfilesFollowTheDesktopRootsEnabledFlag(t *testing.T) {
	profile := t.TempDir()
	write(t, profile, "data/claude-code-sessions/acct/org/local_a.json", `{"cliSessionId":"s1"}`)

	staging := t.TempDir()
	m := config.Machine{Name: "mbp", OS: pathmap.OSMacOS, Home: "/Users/john"}
	if _, err := Sync(Options{
		StagingDir: staging, Config: desktopProfileConfig(t.TempDir(), false), Machine: m,
		Profiles:       []string{"work"},
		SourceOverride: override("desktop@work", profile),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(staging, "desktop@work")); err == nil {
		t.Fatal("profile synced with the desktop root disabled — turning Desktop sync off must cover profiles")
	}
}

func TestSync_KeepFilterAppliesInsideProfiles(t *testing.T) {
	profile := t.TempDir()
	write(t, profile, "data/config.json", `{"preferences":{"theme":"dark"},"oauth":{"tokenCache":"secret-blob"}}`)

	staging := t.TempDir()
	m := config.Machine{Name: "mbp", OS: pathmap.OSMacOS, Home: "/Users/john"}
	if _, err := Sync(Options{
		StagingDir: staging, Config: desktopProfileConfig(t.TempDir(), true), Machine: m,
		Profiles:       []string{"work"},
		SourceOverride: override("desktop@work", profile),
	}); err != nil {
		t.Fatal(err)
	}
	got := read(t, filepath.Join(staging, "desktop@work", "data", "config.json"))
	if want := "theme"; !contains(got, want) {
		t.Errorf("staged config.json = %s, want it to keep preferences", got)
	}
	if contains(got, "tokenCache") {
		t.Errorf("staged config.json = %s, want the volatile token cache dropped", got)
	}
}

func TestRestore_WritesBackIntoTheProfileDataDir(t *testing.T) {
	staging := t.TempDir()
	write(t, filepath.Join(staging, "desktop@work"), "profile.json", `{"name":"work"}`)
	write(t, filepath.Join(staging, "desktop@work"), "data/claude-code-sessions/acct/org/local_a.json", `{"cliSessionId":"s1"}`)
	target := t.TempDir()

	m := config.Machine{Name: "mbp", OS: pathmap.OSMacOS, Home: "/Users/john"}
	rep, err := Restore(RestoreOptions{
		StagingDir: staging, Config: desktopProfileConfig(t.TempDir(), true), Machine: m,
		Profiles:       []string{"work"},
		TargetOverride: override("desktop@work", target),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(target, "data", "claude-code-sessions", "acct", "org", "local_a.json")); err != nil {
		t.Fatalf("sidecar not restored: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "profile.json")); err != nil {
		t.Fatalf("profile record not restored: %v", err)
	}
	if rep.DesktopSessions() == 0 {
		t.Error("restored profile sessions not counted — the restart nudge would never fire")
	}
}

func TestStagedProfileNames_RejectsNamesThatCouldEscape(t *testing.T) {
	staging := t.TempDir()
	for _, dir := range []string{"cli", "desktop", "desktop@work", "desktop@..", "desktop@", "desktop@a b"} {
		if err := os.MkdirAll(filepath.Join(staging, dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	got := StagedProfileNames(staging)
	if len(got) != 1 || got[0] != "work" {
		t.Fatalf("StagedProfileNames = %v, want [work]", got)
	}
}

func TestProfileRootIDRoundTrips(t *testing.T) {
	if got := ProfileNameOf(ProfileRootID("work")); got != "work" {
		t.Fatalf("round trip = %q, want \"work\"", got)
	}
	for _, id := range []string{"cli", "desktop"} {
		if got := ProfileNameOf(id); got != "" {
			t.Errorf("ProfileNameOf(%q) = %q, want \"\"", id, got)
		}
	}
	// The engine mints the id and the allowlist package classifies it; nothing
	// but this test stops the two spellings of "desktop@" from drifting apart,
	// and a drift would quietly hand a profile the CLI allowlist.
	if !allowlist.DesktopRoot(ProfileRootID("work")) {
		t.Error("a profile root is not recognised as a Desktop tree — it would get the CLI allowlist")
	}
	if !allowlist.DesktopRoot("desktop") || allowlist.DesktopRoot("cli") {
		t.Error("allowlist.DesktopRoot must cover the desktop root and nothing of the CLI's")
	}
}

// A restore is the only path that materialises a profile without going through
// the profile store, so it is the only path that can silently weaken the store's
// containment. On a fresh machine the whole profile — chat history included —
// would otherwise land world-readable.
func TestRestore_ProfileFilesKeepTheStoresPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Go's Chmod on Windows only toggles read-only; containment there is the inherited ACL")
	}
	staging := t.TempDir()
	write(t, filepath.Join(staging, "desktop@work"), "profile.json", `{"name":"work"}`)
	write(t, filepath.Join(staging, "desktop@work"), "data/claude-code-sessions/acct/org/local_a.json", `{"cliSessionId":"s1"}`)
	target := filepath.Join(t.TempDir(), "work")

	m := config.Machine{Name: "mbp", OS: pathmap.OSMacOS, Home: "/Users/john"}
	if _, err := Restore(RestoreOptions{
		StagingDir: staging, Config: desktopProfileConfig(t.TempDir(), true), Machine: m,
		Profiles:       []string{"work"},
		TargetOverride: override("desktop@work", target),
	}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path string
		want os.FileMode
	}{
		{filepath.Join(target, "profile.json"), 0o600},
		{filepath.Join(target, "data", "claude-code-sessions", "acct", "org", "local_a.json"), 0o600},
		{filepath.Join(target, "data", "claude-code-sessions", "acct"), 0o700},
	} {
		info, err := os.Stat(tc.path)
		if err != nil {
			t.Fatalf("%s: %v", tc.path, err)
		}
		if got := info.Mode().Perm(); got != tc.want {
			t.Errorf("%s mode = %o, want %o", tc.path, got, tc.want)
		}
	}
}
