package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/core/pathmap"
	"github.com/rigsmith/rigsmith/internal/agentrig/redact"
	"github.com/rigsmith/rigsmith/internal/codexrig/config"
	"github.com/rigsmith/rigsmith/internal/codexrig/manifest"
)

// Every secret below is synthetic and obviously so. The point of putting one in
// at all is that the tests which matter most here are the ones asserting it did
// not come out the other end.

const fakeToken = "sk-fake-notarealkey-000000000000000000000000000000000000000000"

type machine struct {
	home    string // the machine's HOME
	codex   string // its ~/.codex
	name    string
	osToken string
}

func newMachine(t *testing.T, name string) machine {
	t.Helper()
	home := t.TempDir()
	codex := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codex, 0o700); err != nil {
		t.Fatal(err)
	}
	return machine{home: home, codex: codex, name: name, osToken: config.OSToken()}
}

func (m machine) cfg(sessions bool) (*config.Config, config.Machine) {
	c := config.Default()
	c.SyncSessions = sessions
	// Redaction of conversation text is opt-in; the tests that want it say so.
	c.RedactTranscripts = false
	return c, config.Machine{Name: m.name, OS: m.osToken, Home: m.home}
}

func (m machine) write(t *testing.T, rel, content string) string {
	t.Helper()
	p := filepath.Join(m.codex, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// seedTypicalHome lays down the shape of a real ~/.codex: config with a secret
// and an absolute path, instructions, a skill, a credential, a hot database, and
// a plugin tree.
func seedTypicalHome(t *testing.T, m machine) {
	t.Helper()
	m.write(t, "config.toml", `model = "gpt-6-astra"

[features]
js_repl = false

[mcp_servers.railway]
command = "railway"
args = ["mcp", "proxy"]

[mcp_servers.railway.env]
RAILWAY_TOKEN = "`+fakeToken+`"

[projects."`+m.home+`/Git/thing"]
trust_level = "trusted"

[hooks.state."`+m.home+`/Git/thing/.codex/hooks.json:pre_tool_use:0:0"]
trusted_hash = "sha256:0000000000000000000000000000000000000000000000000000000000000000"
`)
	m.write(t, "AGENTS.md", "# house rules\nBe brief.\n")
	m.write(t, "skills/mine/SKILL.md", "# my skill\n")
	m.write(t, "skills/.system/review-agent/SKILL.md", "# codex ships this\n")
	m.write(t, "rules/default.rules", `prefix_rule(pattern=["ls"], decision="allow")`+"\n")
	m.write(t, "auth.json", `{"auth_mode":"chatgpt","tokens":{"refresh_token":"fake-refresh-0001"}}`)
	m.write(t, "state_5.sqlite", "SQLite format 3\x00fake")
	m.write(t, "state_5.sqlite-wal", "fake wal")
	m.write(t, "plugins/cache/mp/plug/1.0/index.js", "console.log(1)")
	m.write(t, "installation_id", "00000000-0000-0000-0000-000000000000")
	m.write(t, "log/codex-login.log", "noise")
}

const rolloutRel = "sessions/2026/09/05/rollout-2026-09-05T11-22-59-01a0722a-7356-7592-922a-336289bdc101.jsonl"

func seedRollout(t *testing.T, m machine) {
	t.Helper()
	meta := `{"timestamp":"2026-09-05T11:22:59.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"01a0722a-7356-7592-922a-336289bdc101","timestamp":"2026-09-05T11:22:59.016Z","cwd":"` + m.home + `/Git/thing","cli_version":"0.144.6","source":"vscode","git":{"branch":"main"}}}`
	msg := `{"timestamp":"2026-09-05T11:23:00.000Z","ordinal":1,"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"review the launcher"}]}}`
	m.write(t, rolloutRel, meta+"\n"+msg+"\n")
}

func syncInto(t *testing.T, m machine, staging string, sessions bool) *Report {
	t.Helper()
	cfg, mc := m.cfg(sessions)
	rep, err := Sync(Options{
		StagingDir:     staging,
		Config:         cfg,
		Machine:        mc,
		CodexVersion:   "0.144.6",
		MaxFileBytes:   config.DefaultMaxFileBytes,
		LargeFileBytes: config.DefaultLargeFileBytes,
		SourceOverride: map[string]string{config.RootCLI: m.codex},
	})
	if err != nil {
		t.Fatalf("sync: %v (findings: %+v)", err, rep.Findings)
	}
	return rep
}

func stagedPath(staging, rel string) string {
	return filepath.Join(staging, config.RootCLI, filepath.FromSlash(rel))
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSyncNeverStagesTheCredentialOrTheHotDatabases(t *testing.T) {
	m := newMachine(t, "one")
	seedTypicalHome(t, m)
	staging := t.TempDir()
	syncInto(t, m, staging, false)

	for _, rel := range []string{"auth.json", "state_5.sqlite", "state_5.sqlite-wal", "installation_id", "log/codex-login.log", "plugins/cache/mp/plug/1.0/index.js", "skills/.system/review-agent/SKILL.md"} {
		if _, err := os.Stat(stagedPath(staging, rel)); err == nil {
			t.Errorf("%s reached the staging repo", rel)
		}
	}
	// And the whole tree, in case something landed somewhere unexpected.
	files, err := listStagedFiles(staging)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.Contains(f, "auth.json") {
			t.Errorf("a file named auth.json is in the tree at %s", f)
		}
	}
}

func TestSyncRedactsAnMCPTokenAndPortablizesPaths(t *testing.T) {
	m := newMachine(t, "one")
	seedTypicalHome(t, m)
	staging := t.TempDir()
	rep := syncInto(t, m, staging, false)

	got := read(t, stagedPath(staging, "config.toml"))
	if strings.Contains(got, fakeToken) {
		t.Fatalf("the MCP token was published:\n%s", got)
	}
	if !strings.Contains(got, redact.Placeholder) {
		t.Errorf("the field was dropped rather than marked, so a restore could not keep the local value:\n%s", got)
	}
	if strings.Contains(got, m.home) {
		t.Errorf("this machine's home leaked into the staged config as a literal path:\n%s", got)
	}
	if !strings.Contains(got, "$HOME") {
		t.Errorf("the project path was not portablized:\n%s", got)
	}
	if rep.Roots[0].Redactions == 0 {
		t.Error("the report claims nothing was redacted")
	}
}

func TestSyncDropsTheHooksTrustTableAndKeepsProjectTrust(t *testing.T) {
	m := newMachine(t, "one")
	seedTypicalHome(t, m)
	staging := t.TempDir()
	syncInto(t, m, staging, false)

	got := read(t, stagedPath(staging, "config.toml"))
	if strings.Contains(got, "trusted_hash") {
		// The key embeds an absolute path AND a content hash of a file that may
		// not exist elsewhere. Restoring it grants nothing.
		t.Errorf("the hook trust table travelled:\n%s", got)
	}
	if !strings.Contains(got, "trust_level") {
		// Kept deliberately: dropping it ends a restore with the user
		// re-trusting every repository by hand.
		t.Errorf("project trust was dropped, which makes a restore much less useful:\n%s", got)
	}
}

func TestSyncIsIncrementalAndReportsNoChangeOnASecondRun(t *testing.T) {
	m := newMachine(t, "one")
	seedTypicalHome(t, m)
	// Age the source files. The stage clock deliberately distrusts an mtime
	// from inside the run that read it — two writes within one filesystem tick
	// are indistinguishable, so the second would be invisible — and a test that
	// writes its fixtures microseconds before syncing them sits exactly in that
	// window. Ageing them puts the test on the path a real machine is on.
	ageTree(t, m.codex, time.Minute)
	staging := t.TempDir()
	first := syncInto(t, m, staging, false)
	if first.Files() == 0 {
		t.Fatal("the first sync wrote nothing")
	}
	second := syncInto(t, m, staging, false)
	if second.Files() != 0 {
		t.Errorf("the second sync rewrote %d file(s) with nothing changed — a derived file's timestamp is not evidence its content moved", second.Files())
	}
	if second.Unchanged() == 0 {
		t.Error("the second sync reported nothing as unchanged either; it did not see the tree")
	}
}

func TestSyncRefusesWhenTheTripwireFindsACredential(t *testing.T) {
	m := newMachine(t, "one")
	seedTypicalHome(t, m)
	// A key pasted into a skill file: not a field the redactor knows by name,
	// which is exactly the case the tripwire exists for.
	m.write(t, "skills/mine/notes.md", "remember: "+fakeToken+"\n")

	cfg, mc := m.cfg(false)
	staging := t.TempDir()
	rep, err := Sync(Options{
		StagingDir: staging, Config: cfg, Machine: mc,
		MaxFileBytes:   config.DefaultMaxFileBytes,
		SourceOverride: map[string]string{config.RootCLI: m.codex},
	})
	if err == nil {
		t.Fatal("the sync completed with a credential in the tree")
	}
	if len(rep.Findings) == 0 {
		t.Fatal("the sync failed without saying what it found")
	}
	if !strings.Contains(err.Error(), "tripwire") {
		t.Errorf("error = %v, want it to name the tripwire", err)
	}
	if _, statErr := os.Stat(stagedPath(staging, "skills/mine/notes.md")); statErr == nil {
		t.Error("the refused file was staged anyway, so the next run would find it again forever")
	}
}

func TestRolloutsAreOptInAndCarryTheirCwdIntoTheManifest(t *testing.T) {
	m := newMachine(t, "one")
	seedTypicalHome(t, m)
	seedRollout(t, m)

	off := t.TempDir()
	syncInto(t, m, off, false)
	if _, err := os.Stat(stagedPath(off, rolloutRel)); err == nil {
		t.Error("a rollout synced without the user asking for sessions")
	}

	on := t.TempDir()
	syncInto(t, m, on, true)
	if _, err := os.Stat(stagedPath(on, rolloutRel)); err != nil {
		t.Fatalf("the rollout did not sync: %v", err)
	}
	man, err := manifest.Load(on)
	if err != nil {
		t.Fatal(err)
	}
	want := m.home + "/Git/thing"
	tmpl, ok := man.Cwds[want]
	if !ok {
		t.Fatalf("the rollout's working directory is not in the manifest: %+v", man.Cwds)
	}
	if !strings.HasPrefix(tmpl, "$HOME") {
		t.Errorf("the manifest recorded a machine-specific path %q rather than a portable one", tmpl)
	}
}

func TestRolloutBytesAreNeverEdited(t *testing.T) {
	// The rule the whole engine is shaped around: a rollout is a conversation,
	// and rewriting it to make a path look local is editing the artifact in
	// order to back it up.
	m := newMachine(t, "one")
	seedTypicalHome(t, m)
	seedRollout(t, m)
	staging := t.TempDir()
	syncInto(t, m, staging, true)

	src := read(t, filepath.Join(m.codex, filepath.FromSlash(rolloutRel)))
	staged := read(t, stagedPath(staging, rolloutRel))
	if src != staged {
		t.Error("the staged rollout differs from the live one; it must be carried byte for byte")
	}
	if !strings.Contains(staged, m.home) {
		t.Error("the rollout's recorded cwd was rewritten — that is conversation content")
	}
}

func TestARestoreOntoASecondMachineResolvesPathsAndKeepsItsOwnSecret(t *testing.T) {
	one := newMachine(t, "one")
	seedTypicalHome(t, one)
	seedRollout(t, one)
	staging := t.TempDir()
	syncInto(t, one, staging, true)

	// A second machine, with a different home and its own real token already in
	// place under the same field.
	two := newMachine(t, "two")
	two.write(t, "config.toml", "model = \"older\"\n\n[mcp_servers.railway.env]\nRAILWAY_TOKEN = \"two-own-real-value\"\n")

	cfg, mc := two.cfg(true)
	man, err := manifest.Load(staging)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := Restore(RestoreOptions{
		StagingDir: staging, Config: cfg, Machine: mc, Manifest: man,
		TargetOverride: map[string]string{config.RootCLI: two.codex},
	})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Written() == 0 {
		t.Fatal("the restore wrote nothing")
	}

	got := read(t, filepath.Join(two.codex, "config.toml"))
	if !strings.Contains(got, "two-own-real-value") {
		t.Errorf("the second machine's own token was clobbered:\n%s", got)
	}
	if strings.Contains(got, redact.Placeholder) {
		t.Errorf("the sentinel was written into a live config; Codex would send it as a token:\n%s", got)
	}
	if strings.Contains(got, one.home) {
		t.Errorf("the first machine's home was restored literally onto the second:\n%s", got)
	}
	if !strings.Contains(got, two.home) {
		t.Errorf("the portable path was not resolved onto this machine:\n%s", got)
	}
	if !strings.Contains(got, "gpt-6-astra") {
		t.Errorf("a non-secret field did not come across:\n%s", got)
	}
	// The credential is not in the backup, so a restore cannot produce one.
	if _, err := os.Stat(filepath.Join(two.codex, "auth.json")); err == nil {
		t.Error("a restore created an auth.json — the second machine must log in for itself")
	}
	if _, err := os.Stat(filepath.Join(two.codex, filepath.FromSlash(rolloutRel))); err != nil {
		t.Errorf("the rollout did not restore: %v", err)
	}
}

func TestRestoreNeverOverwritesARolloutThatIsAlreadyHere(t *testing.T) {
	// It is append-only, so the local copy is at least as complete — and it may
	// be the file a live session is writing into right now.
	one := newMachine(t, "one")
	seedTypicalHome(t, one)
	seedRollout(t, one)
	staging := t.TempDir()
	syncInto(t, one, staging, true)

	two := newMachine(t, "two")
	local := "LOCAL COPY, LONGER, STILL BEING WRITTEN\n"
	two.write(t, rolloutRel, local)

	cfg, mc := two.cfg(true)
	rep, err := Restore(RestoreOptions{
		StagingDir: staging, Config: cfg, Machine: mc,
		TargetOverride: map[string]string{config.RootCLI: two.codex},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(two.codex, filepath.FromSlash(rolloutRel))); got != local {
		t.Error("a restore overwrote a rollout that was already on this machine")
	}
	if rep.Roots[0].Kept == 0 {
		t.Error("the report did not say the local rollout was kept")
	}
}

func TestRestoreLeavesAnUnchangedConfigAloneSoItsCommentsSurvive(t *testing.T) {
	one := newMachine(t, "one")
	one.write(t, "config.toml", "model = \"gpt-6-astra\"\n")
	staging := t.TempDir()
	syncInto(t, one, staging, false)

	two := newMachine(t, "two")
	commented := "# my own note, which TOML cannot round-trip\nmodel = \"gpt-6-astra\"\n"
	two.write(t, "config.toml", commented)

	cfg, mc := two.cfg(false)
	rep, err := Restore(RestoreOptions{
		StagingDir: staging, Config: cfg, Machine: mc,
		TargetOverride: map[string]string{config.RootCLI: two.codex},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(two.codex, "config.toml")); got != commented {
		t.Errorf("a restore with nothing to change rewrote the file and lost its comments:\n%s", got)
	}
	if len(rep.CommentsLost) != 0 {
		t.Errorf("CommentsLost = %v, want empty when nothing was rewritten", rep.CommentsLost)
	}
}

func TestRestoreSaysWhichFilesLostTheirComments(t *testing.T) {
	one := newMachine(t, "one")
	one.write(t, "config.toml", "model = \"changed-upstream\"\n")
	staging := t.TempDir()
	syncInto(t, one, staging, false)

	two := newMachine(t, "two")
	two.write(t, "config.toml", "# a note\nmodel = \"old\"\n")

	cfg, mc := two.cfg(false)
	rep, err := Restore(RestoreOptions{
		StagingDir: staging, Config: cfg, Machine: mc,
		TargetOverride: map[string]string{config.RootCLI: two.codex},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.CommentsLost) == 0 {
		t.Error("a file was genuinely rewritten and its lost comments were not reported")
	}
}

func TestTighteningTheAllowlistRetiresWhatIsAlreadyStaged(t *testing.T) {
	// A rule added to keep something out has no effect on data already in the
	// repo unless staging is reconciled against it.
	m := newMachine(t, "one")
	seedTypicalHome(t, m)
	seedRollout(t, m)
	staging := t.TempDir()
	syncInto(t, m, staging, true)
	if _, err := os.Stat(stagedPath(staging, rolloutRel)); err != nil {
		t.Fatal("setup: the rollout should be staged")
	}

	// Turn sessions off — the same thing a user does in the config.
	rep := syncInto(t, m, staging, false)
	if _, err := os.Stat(stagedPath(staging, rolloutRel)); err == nil {
		t.Error("the rollout stayed in the repo after it stopped being allowed")
	}
	if rep.Roots[0].Disallowed == 0 {
		t.Error("the report did not say anything was retired")
	}
}

func TestRetentionDropsOldRolloutsFromTheTreeAsWellAsOnCopy(t *testing.T) {
	m := newMachine(t, "one")
	seedTypicalHome(t, m)
	seedRollout(t, m)
	staging := t.TempDir()
	syncInto(t, m, staging, true)

	// Age both copies past the window.
	old := timeLongAgo()
	for _, p := range []string{
		filepath.Join(m.codex, filepath.FromSlash(rolloutRel)),
		stagedPath(staging, rolloutRel),
	} {
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}

	cfg, mc := m.cfg(true)
	rep, err := Sync(Options{
		StagingDir: staging, Config: cfg, Machine: mc,
		RetentionDays:  30,
		MaxFileBytes:   config.DefaultMaxFileBytes,
		SourceOverride: map[string]string{config.RootCLI: m.codex},
	})
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if _, err := os.Stat(stagedPath(staging, rolloutRel)); err == nil {
		t.Error("an aged-out rollout is still in the repo")
	}
	if rep.RetentionPruned == 0 && rep.Roots[0].AgedOut == 0 {
		t.Error("nothing was reported as aged out")
	}
	// The LIVE file is never touched: codexrig backs a machine up, it does not
	// delete from it.
	if _, err := os.Stat(filepath.Join(m.codex, filepath.FromSlash(rolloutRel))); err != nil {
		t.Error("retention deleted the live rollout, which is the user's data")
	}
}

func TestScrubbingRewritesTheStagedCopyAndLeavesTheLiveOneAlone(t *testing.T) {
	m := newMachine(t, "one")
	seedTypicalHome(t, m)
	meta := `{"timestamp":"2026-09-05T11:22:59.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"01a0722a-7356-7592-922a-336289bdc101","cwd":"` + m.home + `"}}`
	leak := `{"timestamp":"2026-09-05T11:23:00.000Z","ordinal":1,"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"use ` + fakeToken + ` please"}]}}`
	m.write(t, rolloutRel, meta+"\n"+leak+"\n")

	cfg, mc := m.cfg(true)
	staging := t.TempDir()
	rep, err := Sync(Options{
		StagingDir: staging, Config: cfg, Machine: mc,
		RedactRollouts: true,
		MaxFileBytes:   config.DefaultMaxFileBytes,
		SourceOverride: map[string]string{config.RootCLI: m.codex},
	})
	if err != nil {
		t.Fatalf("sync: %v (findings %+v)", err, rep.Findings)
	}
	staged := read(t, stagedPath(staging, rolloutRel))
	if strings.Contains(staged, fakeToken) {
		t.Errorf("the scrubber left the token in the staged copy:\n%s", staged)
	}
	if !strings.Contains(staged, "session_meta") {
		t.Error("the scrub destroyed the rollout's structure")
	}
	live := read(t, filepath.Join(m.codex, filepath.FromSlash(rolloutRel)))
	if !strings.Contains(live, fakeToken) {
		t.Error("the scrubber edited the LIVE rollout; codexrig backs a machine up, it does not edit it")
	}
}

func TestAnAbsentRootIsReportedNotInvented(t *testing.T) {
	m := newMachine(t, "one")
	if err := os.RemoveAll(m.codex); err != nil {
		t.Fatal(err)
	}
	cfg, mc := m.cfg(false)
	staging := t.TempDir()
	rep, err := Sync(Options{
		StagingDir: staging, Config: cfg, Machine: mc,
		SourceOverride: map[string]string{config.RootCLI: m.codex},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Roots) != 1 || !rep.Roots[0].Absent {
		t.Errorf("roots = %+v, want the root reported as absent", rep.Roots)
	}
}

func TestPortablizeAndResolveAgreeAboutThisMachine(t *testing.T) {
	// The pair the whole cross-machine story rests on. Checked directly so a
	// failure here reads as "path translation broke" rather than as a confusing
	// restore assertion.
	m := newMachine(t, "one")
	folders := pathmap.MapFolders{"HOME": m.home}
	abs := filepath.Join(m.home, "Git", "thing")
	tmpl, ok := pathmap.Portablize(abs, folders, config.OSToken())
	if !ok {
		t.Fatalf("Portablize(%q) failed", abs)
	}
	back := pathmap.NewResolver(folders, config.OSToken(), nil).Resolve(tmpl)
	if !back.IsResolved() || back.Path != abs {
		t.Errorf("round trip gave %q (status %v), want %q", back.Path, back.Status, abs)
	}
}

// ageTree backdates every file under root, so the stage clock will trust their
// mtimes as evidence of their contents.
func ageTree(t *testing.T, root string, by time.Duration) {
	t.Helper()
	when := time.Now().Add(-by)
	err := filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() {
			return nil //nolint:nilerr // a file that vanished is not this helper's problem
		}
		return os.Chtimes(p, when, when)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// timeLongAgo is a timestamp comfortably outside any retention window used here.
func timeLongAgo() time.Time { return time.Now().AddDate(0, 0, -400) }
