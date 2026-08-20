package commands

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/clauderig/devices"
	"github.com/rigsmith/rigsmith/internal/clauderig/search"
	"github.com/rigsmith/rigsmith/internal/clauderig/session"
)

func TestParseWhen(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)

	day, err := parseWhen("2026-08-17", now, false)
	if err != nil || !day.Equal(time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("day = %v %v", day, err)
	}
	// A day given to --until means the WHOLE day, or `--since X --until X` would
	// span one nanosecond and match nothing.
	end, err := parseWhen("2026-08-17", now, true)
	if err != nil || !end.After(time.Date(2026, 8, 17, 23, 59, 59, 0, time.UTC)) ||
		!end.Before(time.Date(2026, 8, 18, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("end-of-day = %v %v", end, err)
	}
	ts, err := parseWhen("2026-08-17T14:30:00Z", now, false)
	if err != nil || !ts.Equal(time.Date(2026, 8, 17, 14, 30, 0, 0, time.UTC)) {
		t.Errorf("timestamp = %v %v", ts, err)
	}
	// Ages count back from the supplied now, days included (time.ParseDuration
	// has no day unit).
	age, err := parseWhen("3d", now, false)
	if err != nil || !age.Equal(now.Add(-72*time.Hour)) {
		t.Errorf("age 3d = %v %v", age, err)
	}
	if h, err := parseWhen("36h", now, false); err != nil || !h.Equal(now.Add(-36*time.Hour)) {
		t.Errorf("age 36h = %v %v", h, err)
	}
	if z, err := parseWhen("", now, false); err != nil || !z.IsZero() {
		t.Errorf("empty should be the zero time: %v %v", z, err)
	}
	if _, err := parseWhen("last tuesday", now, false); err == nil {
		t.Error("unparseable value should error")
	}
}

// writeDatedTranscript writes a transcript and stamps its mtime, which is the
// session's date when there is no Desktop sidecar.
func writeDatedTranscript(t *testing.T, dir, rel, body string, when time.Time) {
	t.Helper()
	writeTestFile(t, dir, rel, body)
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.Chtimes(p, when, when); err != nil {
		t.Fatal(err)
	}
}

// --since/--until narrow by the same instant the result line dates, and the
// sessions they drop are counted rather than silently vanishing.
func TestSearchSessions_TimeWindowFilters(t *testing.T) {
	live := t.TempDir()
	line := `{"type":"user","message":{"content":"the migration plan"}}` + "\n"
	old := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	recent := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	writeDatedTranscript(t, live, "projects/-slug/sess-old.jsonl", line, old)
	writeDatedTranscript(t, live, "projects/-slug/sess-new.jsonl", line, recent)

	targets := []search.Target{{Label: "cli", Dir: live}}
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)

	var out, errw bytes.Buffer
	sc := sessionScope{since: time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC), now: now}
	if err := searchSessions(&out, &errw, testMachine(t.TempDir()), targets, nil, "migration", sc); err != nil {
		t.Fatal(err)
	}
	got := stripANSI(out.String())
	if !strings.Contains(got, "sess-new") {
		t.Errorf("in-window session missing:\n%s", got)
	}
	if strings.Contains(got, "sess-old") {
		t.Errorf("out-of-window session should be filtered out:\n%s", got)
	}
	if !strings.Contains(got, "1 session(s) hidden by filters") {
		t.Errorf("hidden sessions should be accounted for:\n%s", got)
	}

	// The mirror case: --until keeps only the older one.
	out.Reset()
	sc = sessionScope{until: time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC), now: now}
	if err := searchSessions(&out, &errw, testMachine(t.TempDir()), targets, nil, "migration", sc); err != nil {
		t.Fatal(err)
	}
	got = stripANSI(out.String())
	if !strings.Contains(got, "sess-old") || strings.Contains(got, "sess-new") {
		t.Errorf("--until should keep only the older session:\n%s", got)
	}
}

// A session with no date at all cannot honestly be placed inside a window, so a
// time filter drops it — and says how many it dropped that way.
func TestSearchSessions_UndatedDroppedAndNamed(t *testing.T) {
	live := t.TempDir()
	desk := t.TempDir()
	// Title-only match with no transcript anywhere and no lastActivityAt: nothing
	// to date it by.
	writeTestFile(t, desk, "claude-code-sessions/o/u/local_z.json",
		`{"cliSessionId":"sess-undated","title":"Rocket science notes"}`)

	var out, errw bytes.Buffer
	sc := sessionScope{
		since: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		now:   time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC),
	}
	err := searchSessions(&out, &errw, testMachine(t.TempDir()),
		[]search.Target{{Label: "cli", Dir: live}},
		[]session.Root{{Label: "desktop", Base: desk}}, "rocket", sc)
	if err != nil {
		t.Fatal(err)
	}
	got := stripANSI(out.String())
	if strings.Contains(got, "Rocket science notes") {
		t.Errorf("undated session should not pass a time filter:\n%s", got)
	}
	if !strings.Contains(got, "had no date to place") {
		t.Errorf("undated drops must be named, not silent:\n%s", got)
	}
}

// --cwd narrows to sessions whose project directory contains the text.
func TestSearchSessions_CwdFilter(t *testing.T) {
	live := t.TempDir()
	line := func(cwd string) string {
		return `{"type":"user","cwd":"` + cwd + `","message":{"content":"the migration plan"}}` + "\n"
	}
	writeTestFile(t, live, "projects/-Users-x-Git-tweed/sess-a.jsonl", line("/Users/x/Git/tweed"))
	writeTestFile(t, live, "projects/-Users-x-Git-halyard/sess-b.jsonl", line("/Users/x/Git/halyard"))

	var out, errw bytes.Buffer
	sc := sessionScope{cwd: "tweed", now: time.Now()}
	if err := searchSessions(&out, &errw, testMachine(t.TempDir()),
		[]search.Target{{Label: "cli", Dir: live}}, nil, "migration", sc); err != nil {
		t.Fatal(err)
	}
	got := stripANSI(out.String())
	if !strings.Contains(got, "sess-a") {
		t.Errorf("matching cwd should survive:\n%s", got)
	}
	if strings.Contains(got, "sess-b") {
		t.Errorf("non-matching cwd should be filtered out:\n%s", got)
	}
}

func TestRenderCoverage(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	me := devices.Device{Name: "mbp", OS: "macos", LastSync: now.Add(-5 * time.Minute)}
	stale := devices.Device{Name: "air", OS: "macos", LastSync: now.Add(-4 * 24 * time.Hour)}
	fresh := devices.Device{Name: "air", OS: "macos", LastSync: now.Add(-2 * time.Hour)}

	// A machine that hasn't synced in days is exactly the case where "no results"
	// is not the same as "no such chat" — say so, and say since when.
	var out bytes.Buffer
	renderCoverage(&out, sessionScope{devices: []devices.Device{me, stale}, me: "mbp", now: now})
	got := stripANSI(out.String())
	if !strings.Contains(got, "air has not synced since 2026-08-16 12:00 UTC") {
		t.Errorf("stale device should be named with its last sync:\n%s", got)
	}
	if !strings.Contains(got, "clauderig sync") {
		t.Errorf("warning should say how to fix it:\n%s", got)
	}
	if !strings.Contains(got, "mbp 5m ago (this)") {
		t.Errorf("roster should mark this machine:\n%s", got)
	}

	// A device that synced this morning hides nothing — list it, don't warn.
	out.Reset()
	renderCoverage(&out, sessionScope{devices: []devices.Device{me, fresh}, me: "mbp", now: now})
	got = stripANSI(out.String())
	if strings.Contains(got, "has not synced") {
		t.Errorf("fresh device should not be warned about:\n%s", got)
	}
	if !strings.Contains(got, "air 2h ago") {
		t.Errorf("fresh device should still be listed:\n%s", got)
	}

	// One machine means there is no elsewhere for a chat to be — print nothing.
	out.Reset()
	renderCoverage(&out, sessionScope{devices: []devices.Device{me}, me: "mbp", now: now})
	if s := out.String(); s != "" {
		t.Errorf("single-device registry should print no footer, got:\n%s", s)
	}
}

// Bad flag values are rejected before any config or filesystem read, so the error
// is immediate and the same on a machine with no clauderig setup at all.
func TestSearchCmd_RejectsBadFlagCombinations(t *testing.T) {
	run := func(args ...string) error {
		cmd := NewSearchCmd()
		cmd.SetOut(io.Discard)
		cmd.SetErr(io.Discard)
		cmd.SilenceUsage = true
		cmd.SetArgs(args)
		return cmd.Execute()
	}
	if err := run("billing", "--since", "last tuesday"); err == nil {
		t.Error("unparseable --since should error")
	}
	if err := run("billing", "--since", "2026-08-18", "--until", "2026-08-10"); err == nil {
		t.Error("inverted window should error")
	}
	if err := run("billing", "--raw", "--since", "7d"); err == nil {
		t.Error("--raw with a session filter should error")
	}
	if err := run("billing", "--all", "--cwd", "tweed"); err == nil {
		t.Error("--all with a session filter should error")
	}
}
