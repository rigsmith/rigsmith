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
	renderCoverage(&out, sessionScope{devices: []devices.Device{me, stale}, me: "mbp", now: now, liveInScope: true})
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
	renderCoverage(&out, sessionScope{devices: []devices.Device{me, fresh}, me: "mbp", now: now, liveInScope: true})
	got = stripANSI(out.String())
	if strings.Contains(got, "has not synced") {
		t.Errorf("fresh device should not be warned about:\n%s", got)
	}
	if !strings.Contains(got, "air 2h ago") {
		t.Errorf("fresh device should still be listed:\n%s", got)
	}

	// One machine means there is no elsewhere for a chat to be — print nothing.
	out.Reset()
	renderCoverage(&out, sessionScope{devices: []devices.Device{me}, me: "mbp", now: now, liveInScope: true})
	if s := out.String(); s != "" {
		t.Errorf("single-device registry should print no footer, got:\n%s", s)
	}
}

// --repo takes this machine's live roots out of scope, so its OWN unsynced
// sessions are just as invisible as another machine's — the "it's scanned
// directly" exemption does not hold, even when it is the only device.
func TestRenderCoverage_RepoOnlyDoesNotExemptThisMachine(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	meStale := devices.Device{Name: "mbp", OS: "macos", LastSync: now.Add(-3 * 24 * time.Hour)}

	var out bytes.Buffer
	renderCoverage(&out, sessionScope{devices: []devices.Device{meStale}, me: "mbp", now: now, liveInScope: false})
	got := stripANSI(out.String())
	if !strings.Contains(got, "mbp has not synced since") {
		t.Errorf("under --repo this machine's own sync age bounds the search:\n%s", got)
	}
	if strings.Contains(got, "searched live") {
		t.Errorf("nothing was searched live under --repo:\n%s", got)
	}

	// With the live roots back in scope the same machine is exempt.
	out.Reset()
	renderCoverage(&out, sessionScope{devices: []devices.Device{meStale}, me: "mbp", now: now, liveInScope: true})
	if s := stripANSI(out.String()); s != "" {
		t.Errorf("live-scoped single device should print nothing, got:\n%s", s)
	}
}

// An unreadable registry must not look like a verified single-machine setup —
// those mean opposite things and the output was identical.
func TestRenderCoverage_UnavailableRegistrySaysSo(t *testing.T) {
	var out bytes.Buffer
	renderCoverage(&out, sessionScope{devicesUnavailable: true, me: "mbp", now: time.Now(), liveInScope: true})
	got := stripANSI(out.String())
	if !strings.Contains(got, "device coverage unavailable") {
		t.Errorf("a failed registry read should say so:\n%s", got)
	}
}

// A day count large enough to overflow time.Duration wraps NEGATIVE, which
// parseWhen would turn into a FUTURE cutoff that hides every result while looking
// like a perfectly ordinary flag.
func TestParseAge_RejectsOverflowingDayCounts(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	if _, err := parseWhen("106752d", now, false); err == nil {
		t.Error("an overflowing day count should be rejected, not silently wrapped")
	}
	// Just inside the range still works, and is still in the past.
	got, err := parseWhen("106751d", now, false)
	if err != nil {
		t.Fatalf("106751d should parse: %v", err)
	}
	if !got.Before(now) {
		t.Errorf("an age must resolve to the past, got %v", got)
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

// A title-only match records no hit, so it starts with no transcript path. It
// must still be dated and placed from its transcript, or a sidecar that happens
// to carry no lastActivityAt/cwd makes the session look undated and pathless and
// a time filter drops it — with the file sitting right there.
func TestSearchSessions_TitleOnlyMatchIsDatedFromItsTranscript(t *testing.T) {
	live := t.TempDir()
	desk := t.TempDir()
	when := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	writeDatedTranscript(t, live, "projects/-Users-x-Git-tweed/sess-t.jsonl",
		`{"type":"user","cwd":"/Users/x/Git/tweed","message":{"content":"unrelated chatter"}}`+"\n", when)
	// Sidecar with a title and nothing else — no lastActivityAt, no cwd.
	writeTestFile(t, desk, "claude-code-sessions/o/u/local_t.json",
		`{"cliSessionId":"sess-t","title":"Rocket science notes"}`)

	sc := sessionScope{
		since: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		cwd:   "tweed",
		now:   time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC),
	}
	var out, errw bytes.Buffer
	if err := searchSessions(&out, &errw, testMachine(t.TempDir()),
		[]search.Target{{Label: "cli", Dir: live}},
		[]session.Root{{Label: "desktop", Base: desk}}, "rocket", sc); err != nil {
		t.Fatal(err)
	}
	got := stripANSI(out.String())
	if !strings.Contains(got, "Rocket science notes") {
		t.Errorf("title-only match should survive filters its transcript satisfies:\n%s", got)
	}
	if !strings.Contains(got, "2026-07-04") {
		t.Errorf("date should fall back to the transcript mtime:\n%s", got)
	}
	if !strings.Contains(got, "claude --resume sess-t") {
		t.Errorf("its transcript is live, so it stays resumable:\n%s", got)
	}
	if strings.Contains(got, "hidden by filters") {
		t.Errorf("nothing should have been dropped:\n%s", got)
	}
}

// The two roots nest transcripts at different depths — the live root at
// projects/<slug>/<id>.jsonl, the synced repo at cli/projects/<slug>/<id>.jsonl —
// so the "is this the session's own transcript" test has to be measured from the
// projects/ segment. A fixed depth matches everything in one root and nothing in
// the other, silently.
func TestTranscriptPaths_FindsBothRootDepthsAndSkipsSubagents(t *testing.T) {
	live := t.TempDir()
	repo := t.TempDir()
	body := `{"type":"user","message":{"content":"x"}}` + "\n"
	writeTestFile(t, live, "projects/-slug/sess-live.jsonl", body)
	writeTestFile(t, repo, "cli/projects/-slug/sess-repo.jsonl", body)
	writeTestFile(t, repo, "cli/projects/-slug/sess-repo/subagents/agent-a.jsonl", body)

	targets := []search.Target{{Label: "cli", Dir: live}, {Label: "repo", Dir: repo}}

	if got := transcriptPaths(targets, "cli"); len(got) != 1 || got["sess-live"] == "" {
		t.Errorf("live root: %v", got)
	}
	got := transcriptPaths(targets, "repo")
	if len(got) != 1 || got["sess-repo"] == "" {
		t.Errorf("repo root should be found at its own depth: %v", got)
	}
	// The subagent file resolves to sess-repo too; the session's own transcript
	// must win rather than being replaced by it.
	if !strings.HasSuffix(got["sess-repo"], "sess-repo.jsonl") {
		t.Errorf("subagent file was recorded as the session transcript: %v", got)
	}
}
