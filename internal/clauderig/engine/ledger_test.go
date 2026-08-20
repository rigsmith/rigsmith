package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/internal/clauderig/ledger"
)

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

	added, total, err := recordLedger(staging, "mbp")
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
	added, _, err = recordLedger(staging, "mbp")
	if err != nil || added != 0 {
		t.Fatalf("steady state should write nothing: added=%d err=%v", added, err)
	}

	// Retention drops the body; the row must remain.
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if _, total, err = recordLedger(staging, "mbp"); err != nil {
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
	added, total, err := recordLedger(t.TempDir(), "mbp")
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

	added, total, err := recordLedger(staging, "mbp")
	if err != nil || total != 1 {
		t.Fatalf("want exactly one session, got total=%d added=%d err=%v", total, added, err)
	}
	if got := ledger.LoadAll(staging)["sess-1"].Title; !strings.Contains(got, "parent") {
		t.Errorf("parent's own transcript should own the row, got title %q", got)
	}
	// And the steady state writes nothing — the churn half of the same bug.
	if added, _, _ := recordLedger(staging, "mbp"); added != 0 {
		t.Errorf("steady state rewrote %d row(s)", added)
	}
}
