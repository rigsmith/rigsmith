package engine

import (
	"os"
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

// The upgrade case. Tightening the allowlist only changes what the live walk
// offers — so a sandbox staged by an EARLIER sync would otherwise stay tracked,
// keep being pushed, and be handed back out by restore, making the exclusion
// worthless for every machine that already synced one.
func TestSync_ReconcilesSandboxStagedByAnEarlierSync(t *testing.T) {
	liveCli, liveDesk := t.TempDir(), t.TempDir()
	// The live tree now only has the sidecar — the sandbox is no longer offered.
	write(t, liveDesk, "local-agent-mode-sessions/acct/org/local_x.json", `{"title":"t"}`)

	staging := t.TempDir()
	deskStage := filepath.Join(staging, "desktop")
	// Seeded as an older clauderig would have left it.
	write(t, deskStage, "local-agent-mode-sessions/acct/org/local_x.json", `{"title":"old"}`)
	write(t, deskStage, "local-agent-mode-sessions/acct/org/local_x/.audit-key", "secret")
	write(t, deskStage, "local-agent-mode-sessions/acct/org/local_x/audit.jsonl", "{}\n")
	write(t, deskStage, "local-agent-mode-sessions/acct/org/local_x/uploads/statement.pdf", "pdf")
	write(t, deskStage, "local-agent-mode-sessions/acct/org/local_x/outputs/build.py", "code")
	// A sibling that IS still allowed must survive the reconcile.
	write(t, deskStage, "local-agent-mode-sessions/acct/org/artifacts.json", `{"a":1}`)

	john := config.Machine{Name: "john", OS: pathmap.OSMacOS, Home: "/Users/john"}
	rep, err := Sync(Options{
		StagingDir: staging, Config: twoRootConfig(liveCli, liveDesk), Machine: john,
		SourceOverride: override("cli", liveCli, "desktop", liveDesk),
	})
	if err != nil {
		t.Fatalf("sync: %v (findings=%v)", err, rep.Findings)
	}

	sandbox := filepath.Join(deskStage, "local-agent-mode-sessions", "acct", "org", "local_x")
	if _, err := os.Stat(sandbox); !os.IsNotExist(err) {
		t.Error("the previously staged sandbox directory should have been removed")
	}
	for _, keep := range []string{
		filepath.Join(deskStage, "local-agent-mode-sessions", "acct", "org", "local_x.json"),
		filepath.Join(deskStage, "local-agent-mode-sessions", "acct", "org", "artifacts.json"),
	} {
		if _, err := os.Stat(keep); err != nil {
			t.Errorf("still-allowed file should have been kept: %s", filepath.Base(keep))
		}
	}
	var desk *RootResult
	for i := range rep.Roots {
		if rep.Roots[i].ID == "desktop" {
			desk = &rep.Roots[i]
		}
	}
	if desk == nil || desk.Disallowed != 4 {
		t.Errorf("Disallowed = %v, want 4 (audit-key, audit.jsonl, upload, output)", desk)
	}
}

// The reconcile must not touch a root this machine cannot see: skipping a root
// says nothing about whether its staged files are still wanted, and pruning it
// would delete another machine's data.
func TestSync_ReconcileLeavesUnresolvedRootsAlone(t *testing.T) {
	liveCli := t.TempDir()
	staging := t.TempDir()
	write(t, filepath.Join(staging, "desktop"), "claude-code-sessions/a/b/local_1.json", `{"x":1}`)
	write(t, filepath.Join(staging, "desktop"), "Cache/junk", "junk") // not allowed, but not ours to judge

	cfg := config.Default()
	cfg.Roots = []config.Root{
		{ID: "cli", Enabled: true, Location: pathmap.Cascade{Portable: liveCli}},
		{ID: "desktop", Enabled: true, Location: pathmap.Cascade{Portable: filepath.Join(t.TempDir(), "absent")}},
	}
	john := config.Machine{Name: "john", OS: pathmap.OSMacOS, Home: "/Users/john"}
	if _, err := Sync(Options{StagingDir: staging, Config: cfg, Machine: john,
		SourceOverride: map[string]string{"cli": liveCli}}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"claude-code-sessions/a/b/local_1.json", "Cache/junk"} {
		if _, err := os.Stat(filepath.Join(staging, "desktop", filepath.FromSlash(p))); err != nil {
			t.Errorf("staged file under a skipped root must be left alone: %s", p)
		}
	}
}

// A non-JSON credential file inside an allowed tree must abort the sync rather
// than ride it to the remote — the gap that let Desktop's `.audit-key` through.
// It is refused even though nothing about its content is inspectable: it is 51
// bytes of binary whose entropy sits below the JSON scanner's own threshold.
func TestSync_NonJSONCredentialFileTripsWire(t *testing.T) {
	liveCli, liveDesk := t.TempDir(), t.TempDir()
	write(t, liveCli, "skills/s/SKILL.md", "# a skill\nnothing secret here\n")
	write(t, liveCli, "skills/s/.audit-key", "\x8f\x1a\xd4tK\x91\xff\x03A\x7e\xb0\x11\x9e")

	staging := t.TempDir()
	john := config.Machine{Name: "john", OS: pathmap.OSMacOS, Home: "/Users/john"}
	rep, err := Sync(Options{StagingDir: staging, Config: twoRootConfig(liveCli, liveDesk), Machine: john, SourceOverride: override("cli", liveCli, "desktop", liveDesk)})
	if err == nil {
		t.Fatal("sync should have been refused")
	}
	if len(rep.Findings) != 1 || rep.Findings[0].Kind != "key-material" {
		t.Fatalf("findings = %+v, want one key-material finding", rep.Findings)
	}
	if !contains(rep.Findings[0].Path, ".audit-key") {
		t.Errorf("finding should name the file: %s", rep.Findings[0].Path)
	}
	// The offending file must not have been staged on the way to failing.
	if _, err := os.Stat(filepath.Join(staging, "cli", "skills", "s", ".audit-key")); !os.IsNotExist(err) {
		t.Error("credential file should not have been copied into staging")
	}
}

// The wire keeps firing on a secret an EARLIER sync already staged: the
// incremental same-size+mtime skip must not become a way for it to go quiet.
func TestSync_StagedCredentialKeepsTripping(t *testing.T) {
	liveCli, liveDesk := t.TempDir(), t.TempDir()
	// Opaque bytes, deliberately NOT a PEM: this file trips on its NAME, so
	// giving it real key-shaped content would prove nothing and would put a
	// credential-looking literal into the repo for the secret scanner to find.
	const keyish = "\x9f\x2ak\x11opaque-not-a-real-key\x03\x7e"
	write(t, liveCli, "skills/s/id_rsa", keyish)

	staging := t.TempDir()
	// Simulate a copy staged before the check existed, matching size and mtime.
	write(t, filepath.Join(staging, "cli"), "skills/s/id_rsa", keyish)
	src := filepath.Join(liveCli, "skills", "s", "id_rsa")
	info, err := os.Stat(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(staging, "cli", "skills", "s", "id_rsa"), info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}

	john := config.Machine{Name: "john", OS: pathmap.OSMacOS, Home: "/Users/john"}
	rep, err := Sync(Options{StagingDir: staging, Config: twoRootConfig(liveCli, liveDesk), Machine: john, SourceOverride: override("cli", liveCli, "desktop", liveDesk)})
	if err == nil {
		t.Fatal("an already-staged secret must still refuse the sync")
	}
	if len(rep.Findings) != 1 {
		t.Fatalf("findings = %+v, want one", rep.Findings)
	}
}
