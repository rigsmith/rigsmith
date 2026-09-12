package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/core/pathmap"
	"github.com/rigsmith/rigsmith/internal/agentrig/redact"
	"github.com/rigsmith/rigsmith/internal/codexrig/config"
	"github.com/rigsmith/rigsmith/internal/codexrig/ledger"
	"github.com/rigsmith/rigsmith/internal/codexrig/manifest"
	"github.com/rigsmith/rigsmith/internal/codexrig/rollout"
	"github.com/rigsmith/rigsmith/internal/codexrig/rolloutstore"
	"github.com/rigsmith/rigsmith/internal/codexrig/sessions"
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

// project is the fixture's project directory, spelled the way this platform
// spells a path. Windows is the point: C:\Users\… is what Codex records there,
// and it is what PortablizeKeys has to cope with.
func (m machine) project() string { return filepath.Join(m.home, "Git", "thing") }

// tomlPath spells a native path as a TOML LITERAL string. A basic string reads
// the backslashes in C:\Users as escape sequences and the document stops
// parsing — which, on Windows, made the config unreadable, the sync skip it,
// and four tests fail for a reason that had nothing to do with what they check.
// A literal string is also how a person would write a Windows path in TOML.
func tomlPath(p string) string { return "'" + p + "'" }

// jsonPath spells a native path as a JSON string, escaped as JSON requires.
// Codex records the working directory inside the rollout, so the fixture has to
// produce a document that decodes.
func jsonPath(p string) string {
	b, err := json.Marshal(p)
	if err != nil {
		panic(err)
	}
	return string(b)
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

[projects.`+tomlPath(m.project())+`]
trust_level = "trusted"

[hooks.state.`+tomlPath(filepath.Join(m.project(), ".codex", "hooks.json")+":pre_tool_use:0:0")+`]
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
	meta := `{"timestamp":"2026-09-05T11:22:59.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"01a0722a-7356-7592-922a-336289bdc101","timestamp":"2026-09-05T11:22:59.016Z","cwd":` + jsonPath(m.project()) + `,"cli_version":"0.144.6","source":"vscode","git":{"branch":"main"}}}`
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
	want := m.project()
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
	// The cwd as the rollout spells it — JSON-escaped, which on Windows is not
	// the same bytes as the path itself.
	if !strings.Contains(staged, strings.Trim(jsonPath(m.project()), `"`)) {
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
	rep, err := Restore(RestoreOptions{
		StagingDir: staging, Config: cfg, Machine: mc,
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
	// Throw the ledger away, which is the upgrade case: a repo whose rollouts
	// were staged by a codexrig that had no ledger yet. Without it the test
	// passes on the row the FIRST sync wrote, and proves nothing about the
	// order the second one does its work in.
	if err := os.RemoveAll(filepath.Join(staging, ledger.DirName)); err != nil {
		t.Fatal(err)
	}

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
	meta := `{"timestamp":"2026-09-05T11:22:59.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"01a0722a-7356-7592-922a-336289bdc101","cwd":` + jsonPath(m.home) + `}}`
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

func TestAnAgedOutSessionIsStillRememberedAndFindable(t *testing.T) {
	// The ledger's whole reason to exist, and the reason it is written BEFORE
	// retention runs. The other order would let a rollout be pruned in the same
	// sync that should have recorded it, and a later search would answer "no
	// such session" for a conversation that simply got old.
	m := newMachine(t, "one")
	seedTypicalHome(t, m)
	seedRollout(t, m)
	staging := t.TempDir()
	rep := syncInto(t, m, staging, true)
	if rep.LedgerAdded == 0 {
		t.Fatalf("the first sync recorded nothing in the ledger (%s)", rep.LedgerError)
	}

	// Throw the ledger away, which is the upgrade case: a repo whose rollouts
	// were staged by a codexrig that had no ledger yet. Without it the test
	// passes on the row the FIRST sync wrote, and proves nothing about the
	// order the second one does its work in.
	if err := os.RemoveAll(filepath.Join(staging, ledger.DirName)); err != nil {
		t.Fatal(err)
	}

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
	if _, err := Sync(Options{
		StagingDir: staging, Config: cfg, Machine: mc,
		RetentionDays:  30,
		MaxFileBytes:   config.DefaultMaxFileBytes,
		SourceOverride: map[string]string{config.RootCLI: m.codex},
	}); err != nil {
		t.Fatalf("sync: %v", err)
	}

	if _, err := os.Stat(stagedPath(staging, rolloutRel)); err == nil {
		t.Fatal("setup: the rollout should have aged out of the tree")
	}
	remembered := ledger.LoadAll(staging)
	e, ok := remembered["01a0722a-7356-7592-922a-336289bdc101"]
	if !ok {
		t.Fatal("the aged-out rollout was pruned in the same sync that should have recorded it, " +
			"and is now forgotten entirely — a search would say it never existed")
	}
	if e.Title == "" || e.Cwd == "" {
		t.Errorf("row = %+v, want enough to recognise it by", e)
	}
	if e.Shard == "" {
		t.Error("the row does not say where in git history to look for the body")
	}

	// And it comes back out of a listing, marked as remembered rather than
	// presented as something that can be opened.
	rows, _ := sessions.List(sessions.Options{
		Targets: []sessions.Target{{Label: sessions.Repo, Dir: filepath.Join(staging, config.RootCLI)}},
		Ledger:  remembered,
	})
	if len(rows) != 1 {
		t.Fatalf("got %d row(s), want the remembered session", len(rows))
	}
	if !rows[0].Remembered {
		t.Error("a session with no body left should be marked remembered")
	}
	if rows[0].Resumable {
		t.Error("a session with no body cannot be resumed, and must not claim it can")
	}
	if rows[0].Title == "" {
		t.Error("the listing shows nothing to recognise it by")
	}
}

// bigRollout builds a rollout comfortably over the chunking threshold, out of
// records shaped like the real thing so boundaries land mid-record.
func bigRollout(t *testing.T, cwd string, turns int) string {
	t.Helper()
	var b strings.Builder
	b.WriteString(`{"timestamp":"2026-09-05T11:22:59.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"01a0722a-7356-7592-922a-336289bdc101","timestamp":"2026-09-05T11:22:59.016Z","cwd":` + jsonPath(cwd) + `,"cli_version":"0.144.6"}}` + "\n")
	b.WriteString(`{"timestamp":"2026-09-05T11:23:00.000Z","ordinal":1,"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"review the launcher"}]}}` + "\n")
	filler := strings.Repeat("y", 900)
	for i := 0; i < turns; i++ {
		b.WriteString(`{"timestamp":"2026-09-05T11:24:00.000Z","ordinal":2,"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"` + filler + `"}]}}` + "\n")
	}
	return b.String()
}

func TestALargeRolloutIsStoredInPartsAndComesBackWhole(t *testing.T) {
	// The measurement this exists for: on a real machine 59 rollouts total 215
	// MB and ONE of them is 180 MB. That single file is over the default
	// per-file cap, so without this it is the one conversation never backed up.
	m := newMachine(t, "one")
	seedTypicalHome(t, m)
	want := bigRollout(t, m.project(), 12000) // ~11 MB, over the 8 MB threshold
	m.write(t, rolloutRel, want)

	cfg, mc := m.cfg(true)
	staging := t.TempDir()
	rep, err := Sync(Options{
		StagingDir: staging, Config: cfg, Machine: mc,
		ChunkRollouts: true,
		// Deliberately BELOW the rollout's size: a chunked rollout must be
		// exempt, because no blob it produces is anywhere near a host's limit.
		MaxFileBytes:   4 << 20,
		SourceOverride: map[string]string{config.RootCLI: m.codex},
	})
	if err != nil {
		t.Fatalf("sync: %v (findings %+v)", err, rep.Findings)
	}
	if len(rep.Roots[0].Oversize) > 0 {
		t.Fatalf("the rollout was dropped as oversize: %+v", rep.Roots[0].Oversize)
	}

	staged := stagedPath(staging, rolloutRel)
	raw, err := os.ReadFile(staged)
	if err != nil {
		t.Fatal(err)
	}
	if !rolloutstore.IsIndex(raw) {
		t.Fatal("the rollout was staged whole rather than in parts")
	}
	if int64(len(raw)) > 64<<10 {
		t.Errorf("the index is %d bytes; it should be a fraction of the rollout", len(raw))
	}

	// It still reads as the conversation it is, through every reader.
	got, err := rolloutstore.ReadFile(staged)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Error("the chunked rollout does not read back byte for byte")
	}
	if meta, ok, _ := rollout.ReadMeta(staged); !ok || meta.Cwd != m.project() {
		t.Errorf("the header reader could not read a chunked rollout: %+v", meta)
	}
	if title := rollout.FirstPrompt(staged); title != "review the launcher" {
		t.Errorf("FirstPrompt on a chunked rollout = %q", title)
	}
	if act, ok := rollout.LastActivity(staged); !ok || act.At.IsZero() {
		t.Error("the tail reader could not read a chunked rollout")
	}

	// And a restore reconstructs plain JSONL, which is what Codex reads.
	two := newMachine(t, "two")
	cfg2, mc2 := two.cfg(true)
	if _, err := Restore(RestoreOptions{
		StagingDir: staging, Config: cfg2, Machine: mc2,
		TargetOverride: map[string]string{config.RootCLI: two.codex},
	}); err != nil {
		t.Fatal(err)
	}
	restored := read(t, filepath.Join(two.codex, filepath.FromSlash(rolloutRel)))
	if restored != want {
		t.Error("the restored rollout is not the conversation that was captured")
	}
	if rolloutstore.IsIndex([]byte(restored)) {
		t.Fatal("an index was restored onto the machine instead of the rollout")
	}
	// Nothing belonging to the repo's representation leaks onto the machine.
	if _, err := os.Stat(filepath.Join(two.codex, filepath.FromSlash(rolloutRel)) + rolloutstore.Suffix); err == nil {
		t.Error("a parts directory was restored onto the machine")
	}
}

func TestAppendingToALargeRolloutCostsAChunkNotACopy(t *testing.T) {
	m := newMachine(t, "one")
	seedTypicalHome(t, m)
	first := bigRollout(t, m.home, 12000)
	m.write(t, rolloutRel, first)

	cfg, mc := m.cfg(true)
	staging := t.TempDir()
	opts := Options{
		StagingDir: staging, Config: cfg, Machine: mc,
		ChunkRollouts:  true,
		MaxFileBytes:   config.DefaultMaxFileBytes,
		SourceOverride: map[string]string{config.RootCLI: m.codex},
	}
	if _, err := Sync(opts); err != nil {
		t.Fatal(err)
	}
	before := partSet(t, stagedPath(staging, rolloutRel))

	// One more turn, and a flush so the large-file throttle does not defer it.
	grown := first + `{"timestamp":"2026-09-05T11:30:00.000Z","ordinal":3,"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"one more thing"}]}}` + "\n"
	m.write(t, rolloutRel, grown)
	opts.Flush = []string{filepath.Join(m.codex, filepath.FromSlash(rolloutRel))}
	if _, err := Sync(opts); err != nil {
		t.Fatal(err)
	}
	after := partSet(t, stagedPath(staging, rolloutRel))

	kept := 0
	for name := range before {
		if after[name] {
			kept++
		}
	}
	// Every full part is content-addressed and unchanged, so git already has
	// it. Only the last one is rewritten.
	if kept < len(before)-1 {
		t.Errorf("only %d of %d parts survived an append — git would store the whole conversation again", kept, len(before))
	}
	got, err := rolloutstore.ReadFile(stagedPath(staging, rolloutRel))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != grown {
		t.Error("the appended rollout did not read back correctly")
	}
}

func TestTurningChunkingOnConvertsWhatIsAlreadyStaged(t *testing.T) {
	// The setting has to reach what is already in the repo — which is where the
	// large conversations are. Nothing about those files changes when the
	// setting does, so the incremental skip would leave them as single blobs.
	m := newMachine(t, "one")
	seedTypicalHome(t, m)
	m.write(t, rolloutRel, bigRollout(t, m.home, 12000))

	cfg, mc := m.cfg(true)
	staging := t.TempDir()
	base := Options{
		StagingDir: staging, Config: cfg, Machine: mc,
		MaxFileBytes:   config.DefaultMaxFileBytes,
		SourceOverride: map[string]string{config.RootCLI: m.codex},
	}
	if _, err := Sync(base); err != nil { // chunking off
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(stagedPath(staging, rolloutRel)); rolloutstore.IsIndex(raw) {
		t.Fatal("setup: it should have been staged whole")
	}

	on := base
	on.ChunkRollouts = true
	if _, err := Sync(on); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(stagedPath(staging, rolloutRel))
	if err != nil {
		t.Fatal(err)
	}
	if !rolloutstore.IsIndex(raw) {
		t.Error("turning chunking on left the already-staged rollout whole")
	}

	// And back again, because a setting that cannot be undone is a trap.
	if _, err := Sync(base); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(stagedPath(staging, rolloutRel))
	if err != nil {
		t.Fatal(err)
	}
	if rolloutstore.IsIndex(raw) {
		t.Error("turning chunking off left the rollout in parts")
	}
	if _, err := os.Stat(stagedPath(staging, rolloutRel) + rolloutstore.Suffix); err == nil {
		t.Error("the parts directory was left behind")
	}
}

func TestACredentialInsideAChunkedRolloutIsStillCaught(t *testing.T) {
	// The audit must read the conversation, not the index. Scanning a few
	// hundred bytes of hashes would clear a file nobody looked at.
	m := newMachine(t, "one")
	seedTypicalHome(t, m)
	leaky := bigRollout(t, m.home, 12000) +
		`{"timestamp":"2026-09-05T11:31:00.000Z","ordinal":4,"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"use ` + fakeToken + ` please"}]}}` + "\n"
	m.write(t, rolloutRel, leaky)

	cfg, mc := m.cfg(true)
	staging := t.TempDir()
	rep, err := Sync(Options{
		StagingDir: staging, Config: cfg, Machine: mc,
		ChunkRollouts:  true,
		MaxFileBytes:   config.DefaultMaxFileBytes,
		SourceOverride: map[string]string{config.RootCLI: m.codex},
	})
	if err == nil {
		t.Fatal("a credential inside a chunked rollout was published")
	}
	if len(rep.Findings) == 0 {
		t.Fatal("the sync refused without saying what it found")
	}
}

func partSet(t *testing.T, staged string) map[string]bool {
	t.Helper()
	entries, err := os.ReadDir(staged + rolloutstore.Suffix)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".part") {
			out[e.Name()] = true
		}
	}
	return out
}
