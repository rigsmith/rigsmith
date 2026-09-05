package commands

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/clauderig/ledger"
	"github.com/rigsmith/rigsmith/internal/clauderig/search"
	"github.com/rigsmith/rigsmith/internal/clauderig/sessions"
)

// The case the ledger exists for: the transcript has aged out of the synced
// window, so there is no body to scan anywhere — and search must still name the
// session instead of answering "no matching sessions", which reads as "that chat
// never happened".
func TestSearchSessions_LedgerOnlySessionIsFound(t *testing.T) {
	live := t.TempDir()
	when := time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)

	sc := sessions.Scope{
		Now: time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC),
		Ledger: map[string]ledger.Entry{
			"sess-gone": {
				ID: "sess-gone", Title: "the auth refactor", Cwd: "/Users/j/Git/api",
				End: when, Bytes: 1234, RecordedBy: "air",
			},
		},
	}

	var out, errw bytes.Buffer
	if err := searchSessions(&out, &errw, testMachine(t.TempDir()),
		[]search.Target{{Label: "cli", Dir: live}}, nil, "auth refactor", sc, false); err != nil {
		t.Fatal(err)
	}
	got := stripANSI(out.String())

	if !strings.Contains(got, "the auth refactor") {
		t.Errorf("ledger-only session should be named:\n%s", got)
	}
	if !strings.Contains(got, "2026-03-04") {
		t.Errorf("ledger row carries the date, so it should be shown:\n%s", got)
	}
	// The row records the cwd as the machine that ran the session spelled it, and
	// search resolves it onto THIS machine — so on Windows the separators come
	// back native. Asserting the POSIX literal passed everywhere except the one
	// platform the conversion exists for.
	if !strings.Contains(got, filepath.FromSlash("/Users/j/Git/api")) {
		t.Errorf("ledger row carries the project, so it should be shown:\n%s", got)
	}
	// "gone" would be wrong — the blob is still in the sync repo's git history,
	// which is the entire reason the row was kept.
	if !strings.Contains(got, "aged out of the synced window") {
		t.Errorf("should say the body aged out, and that it's recoverable:\n%s", got)
	}
	if !strings.Contains(got, "ledger") {
		t.Errorf("source label should name the ledger:\n%s", got)
	}
	if strings.Contains(got, "claude --resume") {
		t.Errorf("nothing to resume — must not offer a command that fails:\n%s", got)
	}
}

// A session the ledger remembers whose body is still in the synced repo is
// PRESENT, not aged out. Telling someone to go digging in git history for a file
// sitting in their staging tree would be the ledger's most annoying failure mode.
func TestSearchSessions_LedgerDoesNotClaimPresentSessionIsGone(t *testing.T) {
	live := t.TempDir()
	repo := t.TempDir()
	// The body is in the repo but says nothing about the query, so only the
	// ledger title matches — exactly the shape that could be mislabelled.
	writeTestFile(t, repo, "cli/projects/-slug/sess-here.jsonl",
		`{"type":"user","message":{"content":"unrelated chatter"}}`+"\n")

	sc := sessions.Scope{
		Now: time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC),
		Ledger: map[string]ledger.Entry{
			"sess-here": {ID: "sess-here", Title: "the auth refactor", End: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)},
		},
	}

	var out, errw bytes.Buffer
	if err := searchSessions(&out, &errw, testMachine(t.TempDir()),
		[]search.Target{{Label: "cli", Dir: live}, {Label: "repo", Dir: repo}}, nil, "auth refactor", sc, false); err != nil {
		t.Fatal(err)
	}
	got := stripANSI(out.String())
	// Surfaced by the ledger's title, but titled from the transcript itself: when
	// the body is present its own first prompt is the better source, and in
	// practice the two agree — the ledger's title IS that first prompt, recorded
	// earlier. The fixture makes them differ only to show which one wins.
	if !strings.Contains(got, "1 session(s) match") {
		t.Fatalf("ledger title match should surface the session:\n%s", got)
	}
	if !strings.Contains(got, "unrelated chatter") {
		t.Errorf("a present body should title itself, not defer to the ledger row:\n%s", got)
	}
	if strings.Contains(got, "aged out") {
		t.Errorf("body is in the synced repo — must not claim it aged out:\n%s", got)
	}
	if !strings.Contains(got, "synced copy only") {
		t.Errorf("want the ordinary synced-copy note:\n%s", got)
	}
}

// The filters apply to ledger rows too — they carry a date and a project.
func TestSearchSessions_LedgerRowsRespectFilters(t *testing.T) {
	live := t.TempDir()
	sc := sessions.Scope{
		Now:   time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC),
		Since: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		Ledger: map[string]ledger.Entry{
			"old": {ID: "old", Title: "the auth refactor", End: time.Date(2026, 3, 4, 0, 0, 0, 0, time.UTC)},
			"new": {ID: "new", Title: "the auth refactor again", End: time.Date(2026, 7, 4, 0, 0, 0, 0, time.UTC)},
		},
	}
	var out, errw bytes.Buffer
	if err := searchSessions(&out, &errw, testMachine(t.TempDir()),
		[]search.Target{{Label: "cli", Dir: live}}, nil, "auth refactor", sc, false); err != nil {
		t.Fatal(err)
	}
	got := stripANSI(out.String())
	if !strings.Contains(got, "again") {
		t.Errorf("in-window ledger row should survive --since:\n%s", got)
	}
	if strings.Contains(got, "2026-03-04") {
		t.Errorf("out-of-window ledger row should be filtered:\n%s", got)
	}
}
