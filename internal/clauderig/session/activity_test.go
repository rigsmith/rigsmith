package session

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeTranscript(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func mustParse(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return v.UTC()
}

// The whole point: a file touched long after the conversation ended still reports
// the conversation's time. This is the case that breaks every mtime-ordered list
// — a restore or a synced-repo checkout rewrites mtime on hundreds of old chats.
func TestLastActivity_IgnoresMtime(t *testing.T) {
	p := writeTranscript(t,
		`{"type":"user","timestamp":"2026-08-01T10:00:00.000Z"}`+"\n"+
			`{"type":"assistant","timestamp":"2026-08-01T10:05:30.500Z","cwd":"/w","gitBranch":"feat/x"}`+"\n")
	touched := time.Date(2026, 8, 25, 13, 22, 0, 0, time.UTC)
	if err := os.Chtimes(p, touched, touched); err != nil {
		t.Fatal(err)
	}

	a, ok := LastActivity(p)
	if !ok {
		t.Fatal("LastActivity returned !ok for a normal transcript")
	}
	if want := mustParse(t, "2026-08-01T10:05:30.5Z"); !a.At.Equal(want) {
		t.Errorf("At = %s, want %s (the last record, not the mtime %s)", a.At, want, touched)
	}
	if a.Cwd != "/w" || a.GitBranch != "feat/x" {
		t.Errorf("cwd/branch = %q/%q, want /w/feat/x", a.Cwd, a.GitBranch)
	}
}

// Sub-agent records are interleaved into the same file, so the last LINE is not
// guaranteed to be the last MOMENT. The newest timestamp wins regardless of order.
func TestLastActivity_TakesNewestNotLast(t *testing.T) {
	p := writeTranscript(t,
		`{"timestamp":"2026-08-01T10:00:00Z"}`+"\n"+
			`{"timestamp":"2026-08-01T12:00:00Z"}`+"\n"+
			`{"timestamp":"2026-08-01T11:00:00Z","isSidechain":true}`+"\n")
	a, ok := LastActivity(p)
	if !ok {
		t.Fatal("!ok")
	}
	if want := mustParse(t, "2026-08-01T12:00:00Z"); !a.At.Equal(want) {
		t.Errorf("At = %s, want %s", a.At, want)
	}
}

// cwd and branch come from the LAST record carrying them, so a session that moved
// is described by where it ended rather than where it started.
func TestLastActivity_LatestCwdAndBranchWin(t *testing.T) {
	p := writeTranscript(t,
		`{"timestamp":"2026-08-01T10:00:00Z","cwd":"/start","gitBranch":"main"}`+"\n"+
			`{"timestamp":"2026-08-01T11:00:00Z","cwd":"/end","gitBranch":"feat/y"}`+"\n")
	a, _ := LastActivity(p)
	if a.Cwd != "/end" || a.GitBranch != "feat/y" {
		t.Errorf("got %q/%q, want /end/feat/y", a.Cwd, a.GitBranch)
	}
}

// A transcript far larger than the tail window is still read from the end, and the
// record chopped in half by the window boundary must not be parsed as a whole one.
func TestLastActivity_LargeFileReadsFromEnd(t *testing.T) {
	var b strings.Builder
	// Each record is ~1 KiB of padding; 400 of them clears the 64 KiB window well
	// enough that the window opens mid-record.
	pad := strings.Repeat("x", 1000)
	for i := 0; i < 400; i++ {
		fmt.Fprintf(&b, `{"timestamp":"2026-08-01T10:%02d:00Z","pad":"%s"}`+"\n", i%60, pad)
	}
	b.WriteString(`{"timestamp":"2026-08-02T09:30:00Z","cwd":"/late"}` + "\n")
	p := writeTranscript(t, b.String())

	a, ok := LastActivity(p)
	if !ok {
		t.Fatal("!ok")
	}
	if want := mustParse(t, "2026-08-02T09:30:00Z"); !a.At.Equal(want) {
		t.Errorf("At = %s, want %s", a.At, want)
	}
	if a.Cwd != "/late" {
		t.Errorf("cwd = %q, want /late", a.Cwd)
	}
}

// A single record bigger than the initial window (an inlined image) must make the
// window grow rather than making the file look undatable.
func TestLastActivity_SingleRecordLargerThanWindow(t *testing.T) {
	huge := strings.Repeat("y", 200<<10) // 200 KiB, past tailChunkBytes
	p := writeTranscript(t, `{"timestamp":"2026-08-03T08:00:00Z","blob":"`+huge+`"}`+"\n")
	a, ok := LastActivity(p)
	if !ok {
		t.Fatal("a 200 KiB record should be found by growing the window")
	}
	if want := mustParse(t, "2026-08-03T08:00:00Z"); !a.At.Equal(want) {
		t.Errorf("At = %s, want %s", a.At, want)
	}
}

// A crash mid-append leaves a truncated final line. It is skipped, and the last
// COMPLETE record answers — rather than the file being written off as undatable.
func TestLastActivity_TornFinalRecord(t *testing.T) {
	p := writeTranscript(t,
		`{"timestamp":"2026-08-01T10:00:00Z"}`+"\n"+
			`{"timestamp":"2026-08-01T11:0`) // torn, no newline
	a, ok := LastActivity(p)
	if !ok {
		t.Fatal("!ok — a torn tail should fall back to the last whole record")
	}
	if want := mustParse(t, "2026-08-01T10:00:00Z"); !a.At.Equal(want) {
		t.Errorf("At = %s, want %s", a.At, want)
	}
}

// Files with nothing to date report so, leaving the caller to fall back rather
// than inventing a time. The stub case is real: ~/.claude holds small
// "last-prompt" files that carry no conversation at all.
func TestLastActivity_Undatable(t *testing.T) {
	cases := map[string]string{
		"empty":            "",
		"no timestamps":    `{"type":"last-prompt","lastPrompt":"hi","leafUuid":"u"}` + "\n",
		"unparseable":      "not json at all\n",
		"bad time format":  `{"timestamp":"last tuesday"}` + "\n",
		"blank lines only": "\n\n\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if a, ok := LastActivity(writeTranscript(t, body)); ok {
				t.Errorf("ok=true for %s (At=%s) — should have declined to guess", name, a.At)
			}
		})
	}
}

func TestLastActivity_MissingFile(t *testing.T) {
	if _, ok := LastActivity(filepath.Join(t.TempDir(), "nope.jsonl")); ok {
		t.Error("ok=true for a missing file")
	}
}
