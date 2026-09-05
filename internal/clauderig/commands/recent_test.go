package commands

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/clauderig/search"
	"github.com/rigsmith/rigsmith/internal/clauderig/session"
	"github.com/rigsmith/rigsmith/internal/clauderig/sessions"
)

// recentFixture makes a transcript's records and its mtime disagree, which is the
// normal state of a store that has ever been restored or synced.
func recentFixture(t *testing.T, live, id, slug, body string, mtime time.Time) {
	t.Helper()
	rel := "projects/" + slug + "/" + id + ".jsonl"
	writeTestFile(t, live, rel, body)
	p := filepath.Join(live, filepath.FromSlash(rel))
	if err := os.Chtimes(p, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func record(ts, cwd, branch, prompt string) string {
	return recordAs(ts, cwd, branch, "", prompt)
}

func recordAs(ts, cwd, branch, entrypoint, prompt string) string {
	line := `{"type":"user","timestamp":"` + ts + `","cwd":"` + cwd + `","gitBranch":"` + branch +
		`","entrypoint":"` + entrypoint + `","message":{"role":"user","content":"` + prompt + `"}}`
	return line + "\n"
}

func runRecentQuery(t *testing.T, live, query string, sc sessions.Scope, limit int, long bool) string {
	t.Helper()
	var out, errw bytes.Buffer
	targets := []search.Target{{Label: cliTarget, Dir: live}}
	var roots []session.Root
	if err := listRecent(&out, &errw, testMachine(t.TempDir()), targets, roots, sc, query, limit, long); err != nil {
		t.Fatal(err)
	}
	return stripANSI(out.String())
}

func runRecent(t *testing.T, live string, sc sessions.Scope, limit int, long bool) string {
	t.Helper()
	var out, errw bytes.Buffer
	targets := []search.Target{{Label: cliTarget, Dir: live}}
	var roots []session.Root
	if err := listRecent(&out, &errw, testMachine(t.TempDir()), targets, roots, sc, "", limit, long); err != nil {
		t.Fatal(err)
	}
	return stripANSI(out.String())
}

// A chat finished days ago but copied a minute ago must not outrank one finished
// today.
func TestRecent_OrdersByContentNotMtime(t *testing.T) {
	live := t.TempDir()
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	// Ended a week ago, touched most recently of all.
	recentFixture(t, live, "sessold", "-a", record("2026-08-19T09:00:00Z", "/a", "main", "old work"),
		now.Add(-time.Minute))
	// Ended an hour ago, untouched since.
	recentFixture(t, live, "sessnew", "-b", record("2026-08-26T11:00:00Z", "/b", "main", "todays work"),
		now.Add(-time.Hour))

	got := runRecent(t, live, sessions.Scope{Now: now}, 0, false)
	iNew := strings.Index(got, "sessnew")
	iOld := strings.Index(got, "sessold")
	if iNew < 0 || iOld < 0 {
		t.Fatalf("both sessions should be listed:\n%s", got)
	}
	if iNew > iOld {
		t.Errorf("ordered by mtime, not by transcript content:\n%s", got)
	}
}

// A bulk touch re-dates hundreds of old chats to one minute. The window is judged
// on record timestamps, so they stay out of it.
func TestRecent_WindowIgnoresBulkTouch(t *testing.T) {
	live := t.TempDir()
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	touched := now.Add(-30 * time.Minute) // all restored in the same minute
	for _, id := range []string{"stale1", "stale2", "stale3"} {
		recentFixture(t, live, id, "-old", record("2026-07-04T09:00:00Z", "/old", "main", "july work"), touched)
	}
	recentFixture(t, live, "fresh1", "-new", record("2026-08-26T09:30:00Z", "/new", "main", "this morning"),
		now.Add(-2*time.Hour))

	got := runRecent(t, live, sessions.Scope{Now: now, Since: now.Add(-24 * time.Hour)}, 0, false)
	for _, id := range []string{"stale1", "stale2", "stale3"} {
		if strings.Contains(got, id) {
			t.Errorf("%s is 7 weeks old but appeared in a 24h window:\n%s", id, got)
		}
	}
	if !strings.Contains(got, "fresh1") {
		t.Errorf("the genuinely recent session is missing:\n%s", got)
	}
	if !strings.Contains(got, "1 session(s)") {
		t.Errorf("want exactly 1 session:\n%s", got)
	}
}

// The mtime prefilter is an optimisation; one that hides results is a bug.
func TestRecent_PrefilterKeepsUntouchedSession(t *testing.T) {
	live := t.TempDir()
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	ended := now.Add(-3 * time.Hour)
	recentFixture(t, live, "sessok", "-p", record(ended.Format(time.RFC3339), "/p", "main", "work"), ended)

	got := runRecent(t, live, sessions.Scope{Now: now, Since: now.Add(-24 * time.Hour)}, 0, false)
	if !strings.Contains(got, "sessok") {
		t.Errorf("prefilter dropped a session inside the window:\n%s", got)
	}
}

// A file with no timestamped record is still listed — dropping it would answer
// "that chat isn't here" — but marked, so its date is not mistaken for real.
func TestRecent_MarksUndatableSessions(t *testing.T) {
	live := t.TempDir()
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	recentFixture(t, live, "stub1", "-s",
		`{"type":"last-prompt","lastPrompt":"icons fail","leafUuid":"u"}`+"\n", now.Add(-time.Hour))

	got := runRecent(t, live, sessions.Scope{Now: now, Since: now.Add(-24 * time.Hour)}, 0, false)
	if !strings.Contains(got, "stub1") {
		t.Fatalf("undatable session should still be listed:\n%s", got)
	}
	if !strings.Contains(got, "~") {
		t.Errorf("undatable session should be marked with ~:\n%s", got)
	}
	if !strings.Contains(got, "could not be dated from a transcript record") {
		t.Errorf("footer should explain the ~ marker:\n%s", got)
	}
}

// "HEAD" names nothing, so it must not take the branch column.
func TestRecent_SuppressesDetachedHead(t *testing.T) {
	live := t.TempDir()
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	recentFixture(t, live, "sessh", "-h", record("2026-08-26T11:00:00Z", "/h", "HEAD", "work"), now)
	recentFixture(t, live, "sessb", "-b", record("2026-08-26T10:00:00Z", "/b", "feat/real", "work"), now)

	got := runRecent(t, live, sessions.Scope{Now: now}, 0, false)
	if strings.Contains(got, "HEAD") {
		t.Errorf("detached HEAD should not be shown as a branch:\n%s", got)
	}
	if !strings.Contains(got, "feat/real") {
		t.Errorf("a real branch should be shown:\n%s", got)
	}
}

// Truncation must announce itself.
func TestRecent_LimitNamesWhatItDropped(t *testing.T) {
	live := t.TempDir()
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	for _, hour := range []string{"08", "09", "10", "11"} {
		id := "sess" + hour
		recentFixture(t, live, id, "-"+id, record("2026-08-26T"+hour+":00:00Z", "/x", "main", "work"), now)
	}
	got := runRecent(t, live, sessions.Scope{Now: now}, 2, false)
	if !strings.Contains(got, "2 session(s)") {
		t.Errorf("want 2 shown:\n%s", got)
	}
	if !strings.Contains(got, "2 more in this window") {
		t.Errorf("the 2 dropped sessions must be named:\n%s", got)
	}
}

// --long turns a listing into an action, so it needs a runnable command rather
// than the compact view's shortened id.
func TestRecent_LongGivesResumeCommand(t *testing.T) {
	live := t.TempDir()
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	recentFixture(t, live, "sess-full-id", "-r", record("2026-08-26T11:00:00Z", "/r", "main", "work"), now)

	got := runRecent(t, live, sessions.Scope{Now: now}, 0, true)
	if !strings.Contains(got, "claude --resume sess-full-id") {
		t.Errorf("--long should print a resume command with the full id:\n%s", got)
	}
}

// An empty window says so instead of printing an ambiguous nothing.
func TestRecent_EmptyWindowExplainsItself(t *testing.T) {
	live := t.TempDir()
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	recentFixture(t, live, "sessancient", "-a", record("2026-01-01T09:00:00Z", "/a", "main", "work"), now)

	got := runRecent(t, live, sessions.Scope{Now: now, Since: now.Add(-24 * time.Hour)}, 0, false)
	if !strings.Contains(got, "no sessions in that window") {
		t.Errorf("want an explicit empty-window message:\n%s", got)
	}
	if !strings.Contains(got, "--since 7d") {
		t.Errorf("empty result should suggest widening:\n%s", got)
	}
}

// Every client writes into the same tree, so which app ran a session is knowable
// only from the records.
func TestRecent_ShowsClientThatRanIt(t *testing.T) {
	live := t.TempDir()
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	recentFixture(t, live, "sessvs", "-v",
		recordAs("2026-08-26T11:00:00Z", "/v", "main", "claude-vscode", "editor work"), now)
	recentFixture(t, live, "sesscli", "-c",
		recordAs("2026-08-26T10:00:00Z", "/c", "main", "cli", "terminal work"), now)
	recentFixture(t, live, "sesssdk", "-s",
		recordAs("2026-08-26T09:00:00Z", "/s", "main", "sdk-cs", "sdk work"), now)

	got := runRecent(t, live, sessions.Scope{Now: now}, 0, false)
	for _, want := range []string{"vscode", "cli", "sdk-cs"} {
		if !strings.Contains(got, want) {
			t.Errorf("client %q not shown:\n%s", want, got)
		}
	}
	// "claude-" is the internal spelling, not a name anyone uses.
	if strings.Contains(got, "claude-vscode") {
		t.Errorf("entrypoint shown raw instead of as an app name:\n%s", got)
	}
	// --long must carry it too.
	if long := runRecent(t, live, sessions.Scope{Now: now}, 0, true); !strings.Contains(long, "vscode") {
		t.Errorf("--long should name the client:\n%s", long)
	}
}

// A term narrows the window and searches what was said, not just titles.
func TestRecent_QueryNarrowsWindow(t *testing.T) {
	live := t.TempDir()
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	recentFixture(t, live, "sesshit", "-h",
		record("2026-08-26T11:00:00Z", "/h", "main", "wire up the webhook retry"), now)
	recentFixture(t, live, "sessmiss", "-m",
		record("2026-08-26T10:00:00Z", "/m", "main", "rename the css tokens"), now)

	got := runRecentQuery(t, live, "webhook", sessions.Scope{Now: now}, 0, false)
	if !strings.Contains(got, "sesshit") {
		t.Errorf("body match not found:\n%s", got)
	}
	if strings.Contains(got, "sessmiss") {
		t.Errorf("non-matching session should be filtered out:\n%s", got)
	}
	if !strings.Contains(got, `1 of 2 session(s) in the window match "webhook"`) {
		t.Errorf("footer should say how much of the window was searched:\n%s", got)
	}
}

// Time order is what separates this from `search`.
func TestRecent_QueryKeepsTimeOrder(t *testing.T) {
	live := t.TempDir()
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	// Ranked by relevance the older would lead; by time the newer must.
	recentFixture(t, live, "sessold", "-o",
		record("2026-08-26T09:00:00Z", "/o", "main", "deploy deploy deploy"), now)
	recentFixture(t, live, "sessnew", "-n",
		record("2026-08-26T11:00:00Z", "/n", "main", "deploy once"), now)

	got := runRecentQuery(t, live, "deploy", sessions.Scope{Now: now}, 0, false)
	if strings.Index(got, "sessnew") > strings.Index(got, "sessold") {
		t.Errorf("a query must not reorder the list by relevance:\n%s", got)
	}
}

// A query finding nothing must distinguish itself from an empty window.
func TestRecent_QueryNoMatchExplains(t *testing.T) {
	live := t.TempDir()
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	recentFixture(t, live, "sessone", "-a", record("2026-08-26T11:00:00Z", "/a", "main", "work"), now)

	got := runRecentQuery(t, live, "nonexistent", sessions.Scope{Now: now}, 0, false)
	if !strings.Contains(got, "nothing matching") || !strings.Contains(got, "1 session(s) searched") {
		t.Errorf("want a no-match message naming what was searched:\n%s", got)
	}
	if !strings.Contains(got, "clauderig search") {
		t.Errorf("should point at the whole-store search:\n%s", got)
	}
}

// The footer counts what MATCHED, not what fitted on screen — otherwise a capped
// listing reports fewer matches than it found.
func TestRecent_QueryCountIgnoresLimit(t *testing.T) {
	live := t.TempDir()
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	for _, hour := range []string{"08", "09", "10"} {
		recentFixture(t, live, "sess"+hour, "-"+hour,
			record("2026-08-26T"+hour+":00:00Z", "/x", "main", "deploy the thing"), now)
	}
	recentFixture(t, live, "sessmiss", "-m",
		record("2026-08-26T07:00:00Z", "/x", "main", "unrelated"), now)

	got := runRecentQuery(t, live, "deploy", sessions.Scope{Now: now}, 1, false)
	if !strings.Contains(got, `3 of 4 session(s) in the window match "deploy"`) {
		t.Errorf("match count should not be capped by --limit:\n%s", got)
	}
	if !strings.Contains(got, "2 more in this window") {
		t.Errorf("the capped rows must still be named:\n%s", got)
	}
}

// Long mode is documented as full detail, so it must carry the branch the compact
// line shows.
func TestRecent_LongShowsBranch(t *testing.T) {
	live := t.TempDir()
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	recentFixture(t, live, "sessb", "-b",
		record("2026-08-26T11:00:00Z", "/b", "feat/long-mode", "work"), now)

	got := runRecent(t, live, sessions.Scope{Now: now}, 0, true)
	if !strings.Contains(got, "feat/long-mode") {
		t.Errorf("--long dropped the branch:\n%s", got)
	}
}
