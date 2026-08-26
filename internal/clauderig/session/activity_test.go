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

// A file touched long after the conversation ended still reports the
// conversation's time. This is the case that breaks every mtime-ordered list.
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

// Sub-agent records interleave, so the last LINE need not be the last MOMENT.
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

// A session that moved is described by where it ended.
func TestLastActivity_LatestCwdAndBranchWin(t *testing.T) {
	p := writeTranscript(t,
		`{"timestamp":"2026-08-01T10:00:00Z","cwd":"/start","gitBranch":"main"}`+"\n"+
			`{"timestamp":"2026-08-01T11:00:00Z","cwd":"/end","gitBranch":"feat/y"}`+"\n")
	a, _ := LastActivity(p)
	if a.Cwd != "/end" || a.GitBranch != "feat/y" {
		t.Errorf("got %q/%q, want /end/feat/y", a.Cwd, a.GitBranch)
	}
}

// The record chopped in half by the window boundary must not be parsed as whole.
func TestLastActivity_LargeFileReadsFromEnd(t *testing.T) {
	var b strings.Builder
	// 400 padded records clears the window well enough that it opens mid-record.
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

// A record bigger than the initial window must grow it, not look undatable.
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

// A crash mid-append leaves a truncated final line; the last complete record
// still answers.
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

// Files with nothing to date must say so rather than invent a time. The stub
// case is real: ~/.claude holds "last-prompt" files with no conversation.
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

// A window opening exactly on a record boundary must not discard that record.
//
// Growing the window hides this in most files — the next, larger read puts the
// record in the middle rather than at the edge. It bites at maxTailBytes, where
// there is no next read, and the file is then reported undatable.
func TestLastActivity_BoundaryAtMaxWindow(t *testing.T) {
	rec := `{"timestamp":"2026-08-04T07:00:00Z","cwd":"/edge"}` + "\n"
	// Fill the rest of the window with one untimestamped record, so every smaller
	// window misses and the last read lands exactly on rec's first byte.
	padLen := maxTailBytes - len(rec) - len(`{"type":"queue-operation","pad":""}`) - 1
	filler := `{"type":"queue-operation","pad":"` + strings.Repeat("x", padLen) + `"}` + "\n"
	head := `{"type":"queue-operation"}` + "\n"
	body := head + rec + filler
	if len(body)-len(head) != maxTailBytes {
		t.Fatalf("tail is %d bytes, want exactly maxTailBytes (%d)", len(body)-len(head), maxTailBytes)
	}

	a, ok := LastActivity(writeTranscript(t, body))
	if !ok {
		t.Fatal("!ok — the record at the window boundary was discarded")
	}
	if want := mustParse(t, "2026-08-04T07:00:00Z"); !a.At.Equal(want) {
		t.Errorf("At = %s, want %s", a.At, want)
	}
	if a.Cwd != "/edge" {
		t.Errorf("cwd = %q, want /edge", a.Cwd)
	}
}

// The context shown beside a date must describe that date's record, not some
// earlier one that happened to be nearer the end of the file.
func TestLastActivity_ContextMatchesNewestRecord(t *testing.T) {
	p := writeTranscript(t,
		`{"timestamp":"2026-08-05T12:00:00Z","cwd":"/newest","gitBranch":"feat/new"}`+"\n"+
			`{"timestamp":"2026-08-05T09:00:00Z","cwd":"/older","gitBranch":"old"}`+"\n")
	a, ok := LastActivity(p)
	if !ok {
		t.Fatal("!ok")
	}
	if a.Cwd != "/newest" || a.GitBranch != "feat/new" {
		t.Errorf("got %q/%q, want the newest record's /newest and feat/new", a.Cwd, a.GitBranch)
	}
}

// Records carrying a timestamp but no context are real (queue operations), so a
// field the newest record leaves empty falls back rather than blanking.
func TestLastActivity_ContextFallsBackWhenNewestHasNone(t *testing.T) {
	p := writeTranscript(t,
		`{"timestamp":"2026-08-05T09:00:00Z","cwd":"/work","gitBranch":"main"}`+"\n"+
			`{"type":"queue-operation","timestamp":"2026-08-05T12:00:00Z"}`+"\n")
	a, _ := LastActivity(p)
	if want := mustParse(t, "2026-08-05T12:00:00Z"); !a.At.Equal(want) {
		t.Errorf("At = %s, want %s", a.At, want)
	}
	if a.Cwd != "/work" || a.GitBranch != "main" {
		t.Errorf("got %q/%q, want the fallback /work and main", a.Cwd, a.GitBranch)
	}
}
