package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/internal/clauderig/ledger"
)

// allSessions treats every staged transcript as this machine's own — what the
// live walk would report when the machine did originate them.
func allSessions(staging string) map[string]bool {
	out := map[string]bool{}
	matches, _ := filepath.Glob(filepath.Join(staging, "cli", "projects", "*", "*.jsonl"))
	for _, m := range matches {
		out[strings.TrimSuffix(filepath.Base(m), ".jsonl")] = true
	}
	return out
}

func stageTranscriptBody(t *testing.T, staging, slug, id, body string) string {
	t.Helper()
	dir := filepath.Join(staging, "cli", "projects", slug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, id+".jsonl")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// recordLedger reads the staged transcript for its title and project, and — the
// whole point — the row it writes outlives the transcript when retention drops it.
func TestRecordLedger_RowSurvivesRetention(t *testing.T) {
	staging := t.TempDir()
	p := stageTranscriptBody(t, staging, "-Users-j-Git-api", "sess-1",
		`{"type":"user","cwd":"/Users/j/Git/api","message":{"content":"the auth refactor"}}`+"\n")

	added, total, err := recordLedger(staging, "mbp", "", nil)
	if err != nil || added != 1 || total != 1 {
		t.Fatalf("first pass: added=%d total=%d err=%v", added, total, err)
	}
	got := ledger.LoadAll(staging)
	row, ok := got["sess-1"]
	if !ok {
		t.Fatalf("session not recorded: %+v", got)
	}
	if row.Title == "" {
		t.Error("title should come from the transcript's first prompt")
	}
	if row.Cwd != "/Users/j/Git/api" {
		t.Errorf("cwd = %q, want the transcript's recorded cwd", row.Cwd)
	}
	if row.Slug != "-Users-j-Git-api" {
		t.Errorf("slug = %q", row.Slug)
	}

	// Nothing changed → nothing rewritten, which is what keeps an idle sync from
	// producing a commit.
	added, _, err = recordLedger(staging, "mbp", "", nil)
	if err != nil || added != 0 {
		t.Fatalf("steady state should write nothing: added=%d err=%v", added, err)
	}

	// Retention drops the body; the row must remain.
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if _, total, err = recordLedger(staging, "mbp", "", nil); err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Fatalf("ledger should still remember the aged-out session, total=%d", total)
	}
	if _, ok := ledger.LoadAll(staging)["sess-1"]; !ok {
		t.Error("aged-out session vanished from the ledger — the one thing it must not do")
	}
}

// A staging tree with no transcripts at all is an ordinary state (a Desktop-only
// sync, a fresh repo), not an error.
func TestRecordLedger_EmptyStagingIsFine(t *testing.T) {
	added, total, err := recordLedger(t.TempDir(), "mbp", "", nil)
	if err != nil || added != 0 || total != 0 {
		t.Fatalf("added=%d total=%d err=%v", added, total, err)
	}
}

// Subagent transcripts live at projects/<slug>/<id>/subagents/*.jsonl and resolve
// to their PARENT's session id, so recording them would let each subagent
// overwrite the parent's row — and, because the winner differs per pass, rewrite
// the file on every sync forever. Measured on a real tree before the depth guard:
// 822 files collapsing to 574 sessions, with 369 pointless writes per steady-state
// pass.
func TestRecordLedger_IgnoresSubagentTranscripts(t *testing.T) {
	staging := t.TempDir()
	stageTranscriptBody(t, staging, "-Users-j-Git-api", "sess-1",
		`{"type":"user","cwd":"/Users/j/Git/api","message":{"content":"the parent session"}}`+"\n")
	sub := filepath.Join(staging, "cli", "projects", "-Users-j-Git-api", "sess-1", "subagents")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "agent-abc.jsonl"),
		[]byte(`{"type":"user","message":{"content":"a subagent"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	added, total, err := recordLedger(staging, "mbp", "", nil)
	if err != nil || total != 1 {
		t.Fatalf("want exactly one session, got total=%d added=%d err=%v", total, added, err)
	}
	if got := ledger.LoadAll(staging)["sess-1"].Title; !strings.Contains(got, "parent") {
		t.Errorf("parent's own transcript should own the row, got title %q", got)
	}
	// And the steady state writes nothing — the churn half of the same bug.
	if added, _, _ := recordLedger(staging, "mbp", "", nil); added != 0 {
		t.Errorf("steady state rewrote %d row(s)", added)
	}
}

// stageSidecar writes a Desktop sidecar under the account/org path Desktop
// files it at — the path IS the attribution.
func stageAccountSidecar(t *testing.T, staging, root, acct, org, cliSessionID string) {
	t.Helper()
	dir := filepath.Join(staging, root, "claude-code-sessions", acct, org)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"sessionId":"local_x","cliSessionId":"` + cliSessionID + `","title":"t"}`
	if err := os.WriteFile(filepath.Join(dir, "local_x_"+cliSessionID+".json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A CLI transcript carries no account of its own. Desktop's sidecar path is the
// only ground truth, and the syncing machine's login is the fallback for the
// ~97% of sessions no sidecar covers.
func TestRecordLedger_AttributesAccounts(t *testing.T) {
	staging := t.TempDir()
	body := `{"type":"user","cwd":"/Users/j/Git/api","message":{"content":"hi"}}` + "\n"
	stageTranscriptBody(t, staging, "-Users-j-Git-api", "desktop-sess", body)
	stageTranscriptBody(t, staging, "-Users-j-Git-api", "cli-only-sess", body)
	// only one of them was opened through Desktop
	stageAccountSidecar(t, staging, "desktop", "acct-desktop", "org-1", "desktop-sess")

	if _, _, err := recordLedger(staging, "mbp", "acct-live", allSessions(staging)); err != nil {
		t.Fatal(err)
	}
	l, err := ledger.Open(staging, "mbp")
	if err != nil {
		t.Fatal(err)
	}
	if a, s := l.Attribution("desktop-sess"); a != "acct-desktop" || s != ledger.AccountFromDesktop {
		t.Errorf("sidecar session = %q/%q, want acct-desktop/%s", a, s, ledger.AccountFromDesktop)
	}
	if a, s := l.Attribution("cli-only-sess"); a != "acct-live" || s != ledger.AccountFromSync {
		t.Errorf("cli-only session = %q/%q, want acct-live/%s", a, s, ledger.AccountFromSync)
	}
}

// The transcript never changes again once a session ends, so if an unchanged
// transcript were always skipped, a sidecar appearing later could never upgrade
// the guess it was first recorded with.
func TestRecordLedger_SidecarUpgradesAnUnchangedTranscript(t *testing.T) {
	staging := t.TempDir()
	stageTranscriptBody(t, staging, "-Users-j-Git-api", "sess-1",
		`{"type":"user","cwd":"/Users/j/Git/api","message":{"content":"hi"}}`+"\n")

	if _, _, err := recordLedger(staging, "mbp", "acct-live", allSessions(staging)); err != nil {
		t.Fatal(err)
	}
	// Desktop syncs later; the transcript is untouched.
	stageAccountSidecar(t, staging, "desktop", "acct-desktop", "org-1", "sess-1")
	if _, _, err := recordLedger(staging, "mbp", "acct-live", allSessions(staging)); err != nil {
		t.Fatal(err)
	}
	l, _ := ledger.Open(staging, "mbp")
	if a, s := l.Attribution("sess-1"); a != "acct-desktop" || s != ledger.AccountFromDesktop {
		t.Errorf("got %q/%q, want acct-desktop/%s", a, s, ledger.AccountFromDesktop)
	}
}

// Not logged in (or an unreadable identity) must leave rows unattributed rather
// than invent one — an empty account is honest, a guessed one is not.
func TestRecordLedger_NoLiveAccountLeavesRowsUnattributed(t *testing.T) {
	staging := t.TempDir()
	stageTranscriptBody(t, staging, "-Users-j-Git-api", "sess-1",
		`{"type":"user","cwd":"/Users/j/Git/api","message":{"content":"hi"}}`+"\n")
	if _, _, err := recordLedger(staging, "mbp", "", nil); err != nil {
		t.Fatal(err)
	}
	l, _ := ledger.Open(staging, "mbp")
	if a, s := l.Attribution("sess-1"); a != "" || s != "" {
		t.Errorf("got %q/%q, want both empty", a, s)
	}
}

// Profile roots nest the sidecar tree under data/ — both layouts must be read,
// or a profile-only account's sessions would never be attributed.
func TestRecordLedger_ReadsProfileSidecarLayout(t *testing.T) {
	staging := t.TempDir()
	stageTranscriptBody(t, staging, "-Users-j-Git-api", "sess-1",
		`{"type":"user","cwd":"/Users/j/Git/api","message":{"content":"hi"}}`+"\n")
	stageAccountSidecar(t, staging, filepath.Join("desktop@work", "data"), "acct-work", "org-2", "sess-1")
	if _, _, err := recordLedger(staging, "mbp", "", nil); err != nil {
		t.Fatal(err)
	}
	l, _ := ledger.Open(staging, "mbp")
	if a, s := l.Attribution("sess-1"); a != "acct-work" || s != ledger.AccountFromDesktop {
		t.Errorf("got %q/%q, want acct-work/%s", a, s, ledger.AccountFromDesktop)
	}
}

// recordLedger walks the SHARED staged tree, which holds every machine's
// transcripts. The live-account fallback must not claim the ones this machine
// never offered — attribution is sticky, so the first machine to sync would own
// them permanently.
func TestRecordLedger_LiveAccountOnlyClaimsThisMachinesSessions(t *testing.T) {
	staging := t.TempDir()
	body := `{"type":"user","cwd":"/Users/j/Git/api","message":{"content":"hi"}}` + "\n"
	stageTranscriptBody(t, staging, "-Users-j-Git-api", "mine-sess", body)
	stageTranscriptBody(t, staging, "-Users-j-Git-api", "theirs-sess", body)

	// Only one of them came from this machine's own root.
	mine := map[string]bool{"mine-sess": true}
	if _, _, err := recordLedger(staging, "mbp", "acct-live", mine); err != nil {
		t.Fatal(err)
	}
	l, _ := ledger.Open(staging, "mbp")
	if a, s := l.Attribution("mine-sess"); a != "acct-live" || s != ledger.AccountFromSync {
		t.Errorf("own session = %q/%q, want acct-live/%s", a, s, ledger.AccountFromSync)
	}
	if a, s := l.Attribution("theirs-sess"); a != "" || s != "" {
		t.Errorf("another machine's session was claimed: %q/%q", a, s)
	}
}

// A Desktop sidecar is ground truth about ownership, so it still attributes a
// session this machine never ran — that is the whole point of the better source.
func TestRecordLedger_SidecarAttributesEvenAnotherMachinesSession(t *testing.T) {
	staging := t.TempDir()
	stageTranscriptBody(t, staging, "-Users-j-Git-api", "theirs-sess",
		`{"type":"user","cwd":"/Users/j/Git/api","message":{"content":"hi"}}`+"\n")
	stageAccountSidecar(t, staging, "desktop", "acct-desktop", "org-1", "theirs-sess")

	if _, _, err := recordLedger(staging, "mbp", "acct-live", nil); err != nil {
		t.Fatal(err)
	}
	l, _ := ledger.Open(staging, "mbp")
	if a, s := l.Attribution("theirs-sess"); a != "acct-desktop" || s != ledger.AccountFromDesktop {
		t.Errorf("got %q/%q, want acct-desktop/%s", a, s, ledger.AccountFromDesktop)
	}
}
