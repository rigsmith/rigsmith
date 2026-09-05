package sessions

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/ledger"
	"github.com/rigsmith/rigsmith/internal/clauderig/project"
	"github.com/rigsmith/rigsmith/internal/clauderig/search"
	"github.com/rigsmith/rigsmith/internal/clauderig/session"
)

// projectDir is the directory the fixture sessions ran in, and projectSlug the
// folder they are filed under. It has to be absolute *on this platform*: a
// transcript records the path its own machine used, and the resolver normalises
// separators, so a POSIX path under Windows comes back re-slashed and then
// matches neither the assertions nor a search for it.
var (
	projectDir  = fixtureDir()
	projectSlug = project.Flatten(projectDir)
)

func fixtureDir() string {
	if runtime.GOOS == "windows" {
		return `C:\work\api`
	}
	return "/work/api"
}

func testMachine(home string) config.Machine {
	return config.Machine{Name: "test", OS: config.OSToken(), Home: home}
}

func write(t *testing.T, dir, rel, body string) string {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// turn builds one transcript record.
func turn(role, text, ts string) string {
	b, _ := json.Marshal(map[string]any{
		"type": role, "timestamp": ts, "cwd": projectDir, "gitBranch": "feat/x",
		"entrypoint": "cli",
		"message":    map[string]any{"role": role, "content": text},
	})
	return string(b) + "\n"
}

func writeSidecar(t *testing.T, base, acct, id, title string, lastActivity int64) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"cliSessionId": id, "title": title, "lastActivityAt": lastActivity, "cwd": projectDir,
	})
	write(t, base, filepath.ToSlash(filepath.Join("claude-code-sessions", acct, "org", "local_"+id+".json")), string(body))
}

func rowByID(rows []Row, id string) (Row, bool) {
	for _, r := range rows {
		if r.ID == id {
			return r, true
		}
	}
	return Row{}, false
}

// The point of the package: one session that exists in several places is one
// row that names all of them, dated from its transcript and titled from its
// sidecar. Getting this wrong is what made the same chat appear three times.
func TestList_MergesEveryPlaceASessionLives(t *testing.T) {
	live, repo, desk := t.TempDir(), t.TempDir(), t.TempDir()
	id := "aaaaaaaa-1111-4111-8111-aaaaaaaaaaaa"
	body := turn("user", "set up the database", "2026-08-20T09:00:00Z") +
		turn("assistant", "done", "2026-08-20T09:01:00Z") +
		turn("user", "now add the migration", "2026-08-20T09:02:00Z")
	write(t, live, "projects/"+projectSlug+"/"+id+".jsonl", body)
	write(t, repo, "projects/"+projectSlug+"/"+id+".jsonl", body)
	writeSidecar(t, desk, "acct-1", id, "Database work", 1000)

	rows, rep := List(Options{
		Machine: testMachine(t.TempDir()),
		Roots:   []session.Root{{Label: DesktopSource, Base: desk}},
		Targets: []search.Target{{Label: CLISource, Dir: live}, {Label: RepoSource, Dir: repo}},
		Scope:   Scope{Now: time.Now()},
	})

	if len(rows) != 1 {
		t.Fatalf("got %d rows, want the session listed exactly once: %+v", len(rows), rows)
	}
	r := rows[0]
	if r.Title != "Database work" {
		t.Errorf("Title = %q, want the sidecar's", r.Title)
	}
	if r.LastPrompt != "now add the migration" {
		t.Errorf("LastPrompt = %q, want the newest typed prompt", r.LastPrompt)
	}
	if want := time.Date(2026, 8, 20, 9, 2, 0, 0, time.UTC); !r.When.Equal(want) {
		t.Errorf("When = %s, want the last transcript record %s", r.When, want)
	}
	if r.Approx {
		t.Error("Approx = true for a transcript-dated session")
	}
	if r.Cwd != projectDir {
		t.Errorf("Cwd = %q, want %q", r.Cwd, projectDir)
	}
	if r.Branch != "feat/x" {
		t.Errorf("Branch = %q, want feat/x", r.Branch)
	}
	if r.Client != "cli" {
		t.Errorf("Client = %q, want cli", r.Client)
	}
	if !r.CLILive || !r.InRepo || !r.Present {
		t.Errorf("presence flags wrong: live=%v repo=%v present=%v", r.CLILive, r.InRepo, r.Present)
	}
	// All three: transcripts in cli and repo, and the Desktop sidecar counts as
	// the Desktop store holding something. A Desktop Code-tab session keeps its
	// transcript in the shared cli tree, so a transcript-only reading reported
	// "Desktop has nothing" for exactly the sessions Desktop lists.
	if len(r.Sources) != 3 || r.Sources[0] != CLISource || r.Sources[1] != DesktopSource || r.Sources[2] != RepoSource {
		t.Errorf("Sources = %v, want cli, desktop and repo named", r.Sources)
	}
	// Paths stays strictly about transcripts, so "where is the conversation"
	// still has an unambiguous answer.
	if _, ok := r.Paths[DesktopSource]; ok {
		t.Error("a sidecar was recorded as a transcript path")
	}
	// The live copy is the one `claude --resume` opens, so it must be the one
	// the row points at.
	if filepath.Dir(filepath.Dir(filepath.Dir(r.Path))) != live {
		t.Errorf("Path = %q, want the live copy under %q", r.Path, live)
	}
	if rep.Read != 1 {
		t.Errorf("Read = %d, want 1 — the second copy must not be opened again", rep.Read)
	}
}

// Newest first, and the cap is applied after ordering so --limit gives the most
// recent N rather than an arbitrary N.
func TestList_NewestFirstThenLimited(t *testing.T) {
	live := t.TempDir()
	for i, ts := range []string{"2026-08-01T10:00:00Z", "2026-08-03T10:00:00Z", "2026-08-02T10:00:00Z"} {
		id := string(rune('a'+i)) + "aaaaaaa-1111-4111-8111-aaaaaaaaaaaa"
		write(t, live, "projects/-p/"+id+".jsonl", turn("user", "hello", ts))
	}
	rows, rep := List(Options{
		Machine: testMachine(t.TempDir()),
		Targets: []search.Target{{Label: CLISource, Dir: live}},
		Scope:   Scope{Now: time.Now()},
		Limit:   2,
	})
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	if !rows[0].When.After(rows[1].When) {
		t.Errorf("not newest-first: %s then %s", rows[0].When, rows[1].When)
	}
	if want := time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC); !rows[0].When.Equal(want) {
		t.Errorf("newest = %s, want %s", rows[0].When, want)
	}
	if rep.Total != 3 {
		t.Errorf("Total = %d, want 3 — the count before the limit", rep.Total)
	}
}

// A filter that shrinks the list must say what it hid, and separate "outside the
// window" from "could not be dated at all" — they look identical in the output
// and mean opposite things.
func TestList_WindowHidesAndReportsWhy(t *testing.T) {
	live := t.TempDir()
	old := "aaaaaaaa-1111-4111-8111-aaaaaaaaaaaa"
	fresh := "bbbbbbbb-1111-4111-8111-bbbbbbbbbbbb"
	undated := "cccccccc-1111-4111-8111-cccccccccccc"
	write(t, live, "projects/-p/"+old+".jsonl", turn("user", "old", "2026-08-01T10:00:00Z"))
	write(t, live, "projects/-p/"+fresh+".jsonl", turn("user", "fresh", "2026-08-19T10:00:00Z"))
	// No timestamp anywhere, and an mtime inside the window so it is not skipped
	// before it is even read.
	p := write(t, live, "projects/-p/"+undated+".jsonl", `{"type":"queue-operation"}`+"\n")
	inWindow := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	if err := os.Chtimes(p, inWindow, inWindow); err != nil {
		t.Fatal(err)
	}

	rows, rep := List(Options{
		Machine: testMachine(t.TempDir()),
		Targets: []search.Target{{Label: CLISource, Dir: live}},
		Scope: Scope{
			Since: time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC),
			Now:   time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC),
		},
	})

	if _, ok := rowByID(rows, old); ok {
		t.Error("a session before --since was listed")
	}
	if _, ok := rowByID(rows, fresh); !ok {
		t.Error("the session inside the window was dropped")
	}
	// The undated one has an mtime in the window, so it is dated approximately
	// and kept — but it must be marked, not quietly believed.
	if r, ok := rowByID(rows, undated); ok && !r.Approx {
		t.Error("an mtime-dated session was not marked approximate")
	}
	if rep.Skipped == 0 && rep.Hidden == 0 {
		t.Error("nothing was reported as excluded, but a session was")
	}
}

// A session whose transcript has aged out of the synced window still answers,
// from the permanent ledger, rather than vanishing from the list entirely.
func TestList_LedgerOnlySessionSurvives(t *testing.T) {
	id := "dddddddd-1111-4111-8111-dddddddddddd"
	when := time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)
	rows, _ := List(Options{
		Machine: testMachine(t.TempDir()),
		Scope: Scope{
			Now:    time.Now(),
			Ledger: map[string]ledger.Entry{id: {ID: id, Title: "the qbo migration", Cwd: "/work/qbo", End: when}},
		},
	})
	r, ok := rowByID(rows, id)
	if !ok {
		t.Fatal("a ledger-only session vanished from the listing")
	}
	if r.Title != "the qbo migration" {
		t.Errorf("Title = %q, want the ledger's", r.Title)
	}
	if !r.When.Equal(when) {
		t.Errorf("When = %s, want the ledger's %s", r.When, when)
	}
	if r.Present || r.CLILive || r.InRepo {
		t.Error("a session with no transcript anywhere reported as present")
	}
	if !r.Approx {
		t.Error("a ledger-dated session should be marked approximate")
	}
}

// A Claude Desktop Code-tab session keeps its transcript in the shared
// ~/.claude/projects tree and leaves only a sidecar in the Desktop tree. The
// listing must not then report that Desktop holds nothing — that reads as a
// contradiction beside a "desktop@profile" client, and it is the store you
// would delete from to take the session out of Desktop's list.
func TestList_DesktopSidecarCountsAsPresence(t *testing.T) {
	live, desk := t.TempDir(), t.TempDir()
	id := "eeeeeeee-1111-4111-8111-eeeeeeeeeeee"
	write(t, live, "projects/"+projectSlug+"/"+id+".jsonl", turn("user", "hello", "2026-08-20T09:00:00Z"))
	writeSidecar(t, desk, "acct-1", id, "Filed by Desktop", 1000)

	rows, _ := List(Options{
		Machine: testMachine(t.TempDir()),
		Roots:   []session.Root{{Label: DesktopSource, Base: desk}},
		Targets: []search.Target{{Label: CLISource, Dir: live}},
		Scope:   Scope{Now: time.Now()},
	})
	r, ok := rowByID(rows, id)
	if !ok {
		t.Fatal("session missing from the listing")
	}
	if len(r.Sources) != 2 || r.Sources[0] != CLISource || r.Sources[1] != DesktopSource {
		t.Errorf("Sources = %v, want cli and desktop", r.Sources)
	}
	// The conversation is in the cli tree, and only there.
	if r.Paths[CLISource] == "" || r.Paths[DesktopSource] != "" {
		t.Errorf("Paths = %v, want the transcript under cli only", r.Paths)
	}
	// So it is still resumable, which is what CLILive is for.
	if !r.CLILive {
		t.Error("CLILive false for a session whose transcript is in the live tree")
	}
}

// One search box over everything a row shows: you rarely remember whether the
// thing you recall was the title, the last thing you typed, or the directory.
func TestList_TextSearchesEveryVisibleField(t *testing.T) {
	live, desk := t.TempDir(), t.TempDir()
	ids := map[string]string{
		"title":  "11111111-1111-4111-8111-111111111111",
		"prompt": "22222222-2222-4222-8222-222222222222",
		"other":  "33333333-3333-4333-8333-333333333333",
	}
	write(t, live, "projects/"+projectSlug+"/"+ids["title"]+".jsonl", turn("user", "hello", "2026-08-20T09:00:00Z"))
	writeSidecar(t, desk, "acct", ids["title"], "The billing migration", 1000)
	write(t, live, "projects/"+projectSlug+"/"+ids["prompt"]+".jsonl",
		turn("user", "check the billing totals", "2026-08-20T09:00:00Z"))
	write(t, live, "projects/"+projectSlug+"/"+ids["other"]+".jsonl", turn("user", "unrelated work", "2026-08-20T09:00:00Z"))

	run := func(text string) []Row {
		rows, _ := List(Options{
			Machine: testMachine(t.TempDir()),
			Roots:   []session.Root{{Label: DesktopSource, Base: desk}},
			Targets: []search.Target{{Label: CLISource, Dir: live}},
			Scope:   Scope{Now: time.Now()},
			Text:    text,
		})
		return rows
	}

	// The title, and the last prompt, each on their own.
	if rows := run("billing"); len(rows) != 2 {
		t.Errorf("searching \"billing\" gave %d rows, want the title and prompt matches", len(rows))
	}
	// The project directory, as the Project column renders it — the resolved
	// path, not the flattened slug the transcript is filed under.
	if rows := run(filepath.Join("work", "api")); len(rows) != 3 {
		t.Errorf("searching the project gave %d rows, want all three", len(rows))
	}
	if rows := run(projectSlug); len(rows) != 0 {
		t.Error("the on-disk slug is not shown anywhere, so it should not match")
	}
	// And an id, which is what you have when you copied it from somewhere else.
	if rows := run(ids["other"]); len(rows) != 1 || rows[0].ID != ids["other"] {
		t.Errorf("searching by id gave %v", rows)
	}
	// Case-insensitively, and empty text is not a filter.
	if rows := run("BILLING"); len(rows) != 2 {
		t.Error("search should be case-insensitive")
	}
	if rows := run("  "); len(rows) != 3 {
		t.Error("blank text should not filter anything out")
	}
	if rows := run("nothing matches this"); len(rows) != 0 {
		t.Errorf("got %d rows for a term nothing contains", len(rows))
	}
}

// Deep search reads transcript bodies, so it finds sessions whose row shows
// nothing matching — and reports how many times, with the first hit.
func TestList_ContentSearchesTranscriptBodies(t *testing.T) {
	live := t.TempDir()
	hit := "44444444-4444-4444-8444-444444444444"
	miss := "55555555-5555-4555-8555-555555555555"
	// The distinctive term appears only in the ASSISTANT's replies — never in a
	// prompt, so it is nowhere in the row's own fields.
	write(t, live, "projects/-p/"+hit+".jsonl",
		turn("user", "why is this slow", "2026-08-20T09:00:00Z")+
			turn("assistant", "the culprit is lock contention", "2026-08-20T09:01:00Z")+
			turn("assistant", "that culprit shows up under load", "2026-08-20T09:02:00Z"))
	write(t, live, "projects/-p/"+miss+".jsonl", turn("user", "something else entirely", "2026-08-20T09:00:00Z"))

	opts := Options{
		Machine: testMachine(t.TempDir()),
		Targets: []search.Target{{Label: CLISource, Dir: live}},
		Scope:   Scope{Now: time.Now()},
	}

	// Nothing in either ROW says "dropping" — only the bodies do.
	plain := opts
	plain.Text = "culprit"
	if rows, _ := List(plain); len(rows) != 0 {
		t.Errorf("row-field search matched %d rows; the term is only in the body", len(rows))
	}

	deep := opts
	deep.Content = "culprit"
	rows, _ := List(deep)
	if len(rows) != 1 || rows[0].ID != hit {
		t.Fatalf("content search gave %v, want just the session containing it", rows)
	}
	if rows[0].Matches != 2 {
		t.Errorf("Matches = %d, want both assistant lines", rows[0].Matches)
	}
	if rows[0].Snippet == "" {
		t.Error("no snippet returned for a hit")
	}
	if _, ok := rowByID(rows, miss); ok {
		t.Error("a session without the term survived a content search")
	}
}

// One box, the whole session. Text and Content AND together, so passing a word
// to both asks for sessions whose title AND body contain it — which is not what
// typing a word into a search box means.
func TestList_SearchMatchesRowOrBody(t *testing.T) {
	live := t.TempDir()
	// Matches on its title, which is the first human turn.
	byTitle := "aaaaaaaa-1111-4111-8111-000000000001"
	write(t, live, "projects/"+projectSlug+"/"+byTitle+".jsonl",
		turn("user", "pelican migration notes", "2026-08-20T09:00:00Z")+
			turn("assistant", "nothing else here", "2026-08-20T09:01:00Z"))
	// Matches only deep in the body.
	byBody := "aaaaaaaa-1111-4111-8111-000000000002"
	write(t, live, "projects/"+projectSlug+"/"+byBody+".jsonl",
		turn("user", "unrelated heading", "2026-08-20T10:00:00Z")+
			turn("assistant", "deep inside we discuss pelican habits", "2026-08-20T10:01:00Z"))
	// Matches nowhere.
	write(t, live, "projects/"+projectSlug+"/aaaaaaaa-1111-4111-8111-000000000003.jsonl",
		turn("user", "unrelated heading", "2026-08-20T11:00:00Z"))

	rows, _ := List(Options{
		Machine: testMachine(t.TempDir()),
		Targets: []search.Target{{Label: CLISource, Dir: live}},
		Scope:   Scope{Now: time.Now()},
		Search:  "pelican",
	})

	if len(rows) != 2 {
		var got []string
		for _, r := range rows {
			got = append(got, r.Title)
		}
		t.Fatalf("Search returned %d rows (%v), want the title match and the body match", len(rows), got)
	}
	var sawBodyHit, sawTitleHit bool
	for _, r := range rows {
		switch r.ID {
		case byBody:
			sawBodyHit = true
			if r.Matches == 0 || r.Snippet == "" {
				t.Errorf("the body match carries no hit count or snippet, so the row cannot explain itself: %+v", r)
			}
		case byTitle:
			sawTitleHit = true
			// A term found in the title must not cost a transcript read.
			if r.Matches != 0 {
				t.Error("the body was searched even though the title already matched")
			}
		}
	}
	if !sawBodyHit || !sawTitleHit {
		t.Errorf("wrong rows came back: body=%v title=%v", sawBodyHit, sawTitleHit)
	}
}

// A session filed under two project slugs is one session with two transcripts.
// Choosing between them by directory-walk order is alphabetical luck: the copy
// frozen at the moment the work moved sorts first about half the time, and then
// the tool reports a state from that day with no sign a complete copy exists.
// That is how a week of work appeared lost.
func TestTranscriptPathsAll_PrefersTheNewestCopy(t *testing.T) {
	live := t.TempDir()
	id := "aaaaaaaa-1111-4111-8111-cccccccccccc"

	// "aaa" sorts before "zzz", so walk order would pick the stale one.
	write(t, live, "projects/-aaa-old/"+id+".jsonl",
		turn("user", "started here", "2026-08-20T09:00:00Z"))
	write(t, live, "projects/-zzz-new/"+id+".jsonl",
		turn("user", "started here", "2026-08-20T09:00:00Z")+
			turn("assistant", "and kept going", "2026-09-04T18:00:00Z"))

	targets := []search.Target{{Label: CLISource, Dir: live}}
	paths, extra := TranscriptPathsAll(targets, CLISource)

	if got := paths[id]; !strings.Contains(got, "-zzz-new") {
		t.Errorf("chose %q, want the copy whose last record is newest", got)
	}
	if len(extra[id]) != 1 || !strings.Contains(extra[id][0], "-aaa-old") {
		t.Errorf("extra = %v, want the copy that was not chosen", extra[id])
	}
}

// The ordinary case must not pay for the rare one: a session with one
// transcript is reported with no duplicates and no extra reads.
func TestTranscriptPathsAll_SingleCopyHasNoDuplicates(t *testing.T) {
	live := t.TempDir()
	id := "aaaaaaaa-1111-4111-8111-dddddddddddd"
	write(t, live, "projects/-p/"+id+".jsonl", turn("user", "hello", "2026-08-20T09:00:00Z"))

	paths, extra := TranscriptPathsAll(
		[]search.Target{{Label: CLISource, Dir: live}}, CLISource)
	if paths[id] == "" {
		t.Fatal("the only copy was not found")
	}
	if len(extra) != 0 {
		t.Errorf("extra = %v, want none", extra)
	}
}

// A row has to carry the copies it did not choose, or nothing downstream can
// report them and the discard stays silent.
func TestList_RowCarriesDuplicates(t *testing.T) {
	live := t.TempDir()
	id := "aaaaaaaa-1111-4111-8111-eeeeeeeeeeee"
	write(t, live, "projects/-aaa-old/"+id+".jsonl",
		turn("user", "started here", "2026-08-20T09:00:00Z"))
	write(t, live, "projects/-zzz-new/"+id+".jsonl",
		turn("user", "started here", "2026-08-20T09:00:00Z")+
			turn("assistant", "and kept going", "2026-09-04T18:00:00Z"))

	rows, _ := List(Options{
		Machine: testMachine(t.TempDir()),
		Targets: []search.Target{{Label: CLISource, Dir: live}},
		Scope:   Scope{Now: time.Now()},
	})
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want the session listed once", len(rows))
	}
	if len(rows[0].Duplicates) != 1 {
		t.Errorf("Duplicates = %v, want the one copy that was not chosen", rows[0].Duplicates)
	}
}

// The three front ends must not each decide for themselves whether a session is
// filed correctly — disagreeing about whether a conversation is intact is the
// worst thing they could differ on. CheckHealth is the one answer they share.
func TestCheckHealth_ReportsSplitsWithoutProposingARepair(t *testing.T) {
	live := t.TempDir()
	id := "aaaaaaaa-1111-4111-8111-ffffffffffff"
	write(t, live, "projects/-aaa-old/"+id+".jsonl",
		turn("user", "started here", "2026-08-20T09:00:00Z"))
	write(t, live, "projects/-zzz-new/"+id+".jsonl",
		turn("user", "started here", "2026-08-20T09:00:00Z")+
			turn("assistant", "and kept going", "2026-09-04T18:00:00Z"))

	h := CheckHealth([]search.Target{{Label: CLISource, Dir: live}}, nil)
	if h.OK() {
		t.Fatal("a session filed in two places was reported as healthy")
	}
	if len(h.Splits) != 1 {
		t.Fatalf("Splits = %+v, want one", h.Splits)
	}
	if !strings.Contains(h.Splits[0].Keep, "-zzz-new") {
		t.Errorf("Keep = %q, want the newest copy", h.Splits[0].Keep)
	}
	if len(h.Splits[0].Others) != 1 || !strings.Contains(h.Splits[0].Others[0], "-aaa-old") {
		t.Errorf("Others = %v, want the older copy", h.Splits[0].Others)
	}
}

// A machine where nothing has split must say so, and cheaply — no false alarm
// on the ordinary case is what makes the warning worth reading.
func TestCheckHealth_QuietWhenEverythingIsFiledOnce(t *testing.T) {
	live := t.TempDir()
	write(t, live, "projects/-p/aaaaaaaa-1111-4111-8111-0a0a0a0a0a0a.jsonl",
		turn("user", "hello", "2026-08-20T09:00:00Z"))

	h := CheckHealth([]search.Target{{Label: CLISource, Dir: live}}, nil)
	if !h.OK() {
		t.Errorf("healthy machine reported problems: %+v", h)
	}
}

// Parking a copy is destructive, so the safety judgement is the important part:
// a copy wholly contained in the one being kept can go, and one holding turns
// of its own cannot.
func TestDescribeAndConsolidate(t *testing.T) {
	newRecord := func(uuid, text, ts string) string {
		b, _ := json.Marshal(map[string]any{
			"type": "user", "uuid": uuid, "timestamp": ts, "cwd": "/p",
			"isSidechain": false,
			"message":     map[string]any{"role": "user", "content": text},
		})
		return string(b) + "\n"
	}

	t.Run("contained copy is safe to park", func(t *testing.T) {
		live := t.TempDir()
		id := "aaaaaaaa-1111-4111-8111-0b0b0b0b0b0b"
		old := newRecord("u1", "one", "2026-08-20T09:00:00Z")
		new := old + newRecord("u2", "two", "2026-09-04T18:00:00Z")
		oldPath := write(t, live, "projects/-aaa-old/"+id+".jsonl", old)
		newPath := write(t, live, "projects/-zzz-new/"+id+".jsonl", new)

		d := Describe(Split{ID: id, Keep: newPath, Others: []string{oldPath}})
		if !d.Safe || d.Diverged != 0 {
			t.Fatalf("a prefix copy was called unsafe: %+v", d)
		}
		if len(d.Copies) != 2 || d.Copies[0].Lines != 2 || d.Copies[1].Lines != 1 {
			t.Errorf("copies described wrong: %+v", d.Copies)
		}

		park := filepath.Join(t.TempDir(), "parked")
		got, err := Consolidate(Split{ID: id, Keep: newPath, Others: []string{oldPath}}, park)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 {
			t.Fatalf("parked %v, want one file", got)
		}
		if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
			t.Error("the older copy is still in place")
		}
		if _, err := os.Stat(got[0]); err != nil {
			t.Error("the older copy was deleted rather than moved — the whole fault this repairs " +
				"is a conversation that appeared to vanish")
		}
		if _, err := os.Stat(newPath); err != nil {
			t.Error("the kept copy was disturbed")
		}
	})

	t.Run("diverged copies are refused", func(t *testing.T) {
		live := t.TempDir()
		id := "aaaaaaaa-1111-4111-8111-0c0c0c0c0c0c"
		// Both branch from a shared start; each then has a turn the other lacks.
		base := newRecord("u1", "one", "2026-08-20T09:00:00Z")
		a := write(t, live, "projects/-aaa-old/"+id+".jsonl", base+newRecord("uA", "only here", "2026-08-21T09:00:00Z"))
		b := write(t, live, "projects/-zzz-new/"+id+".jsonl", base+newRecord("uB", "only there", "2026-09-04T18:00:00Z"))

		s := Split{ID: id, Keep: b, Others: []string{a}}
		if d := Describe(s); d.Safe || d.Diverged != 1 {
			t.Fatalf("diverged copies were called safe: %+v", d)
		}
		if _, err := Consolidate(s, filepath.Join(t.TempDir(), "parked")); err == nil {
			t.Fatal("Consolidate parked a copy holding turns of its own")
		}
		if _, err := os.Stat(a); err != nil {
			t.Error("the refused copy was moved anyway")
		}
	})
}
