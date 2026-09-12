package sessions

import (
	"github.com/rigsmith/rigsmith/internal/codexrig/rolloutstore"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func rolloutFile(t *testing.T, root, shard, uuid string, records ...string) string {
	t.Helper()
	dir := filepath.Join(root, filepath.FromSlash("sessions/"+shard))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	stamp := "2026-01-01T00-00-00"
	if len(shard) == 10 {
		stamp = shard[0:4] + "-" + shard[5:7] + "-" + shard[8:10] + "T00-00-00"
	}
	p := filepath.Join(dir, "rollout-"+stamp+"-"+uuid+".jsonl")
	body := ""
	for _, r := range records {
		body += r + "\n"
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func meta(uuid, at, cwd string) string {
	return `{"timestamp":"` + at + `","ordinal":0,"type":"session_meta","payload":{"session_id":"` + uuid +
		`","timestamp":"` + at + `","cwd":"` + cwd + `","cli_version":"0.144.6"}}`
}

func userMsg(at, text string) string {
	return `{"timestamp":"` + at + `","ordinal":1,"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"` + text + `"}]}}`
}

const uuidOld = "01a0722a-7356-7592-922a-000000000001"
const uuidNew = "01a0722a-7356-7592-922a-000000000002"

// This is the bug real data found, and the reason the prune is one-sided.
//
// Codex APPENDS to the same rollout when a session is resumed, and the file
// stays in the directory named for the day it STARTED. A rollout on the machine
// this was written against spans eight calendar days. A listing that pruned the
// early side by shard date would hide exactly the long-running sessions somebody
// is most likely to be looking for.
func TestAnOldShardStillHoldsRecentWork(t *testing.T) {
	root := t.TempDir()
	started := time.Now().AddDate(0, 0, -30)
	yesterday := time.Now().AddDate(0, 0, -1)
	shard := started.Format("2006/01/02")
	rolloutFile(t, root, shard, uuidOld,
		meta(uuidOld, started.Format(time.RFC3339), "/repo"),
		userMsg(yesterday.Format(time.RFC3339), "still working on this"),
	)

	rows, rep := List(Options{
		Targets: []Target{{Label: Live, Dir: root}},
		Since:   time.Now().AddDate(0, 0, -2),
	})
	if rep.Read == 0 {
		t.Fatal("the rollout was never opened — its directory was pruned by its start date")
	}
	if len(rows) != 1 {
		t.Fatalf("got %d row(s), want the session that was active yesterday", len(rows))
	}
	if rows[0].When.Before(time.Now().AddDate(0, 0, -2)) {
		t.Errorf("When = %v, want the last record's time, not the shard's", rows[0].When)
	}
}

// The other side is safe, and worth keeping: a session cannot have records from
// before it started, so a shard after the window holds nothing in it.
func TestAShardAfterTheWindowIsSkipped(t *testing.T) {
	root := t.TempDir()
	future := time.Now().AddDate(0, 0, 30)
	rolloutFile(t, root, future.Format("2006/01/02"), uuidNew,
		meta(uuidNew, future.Format(time.RFC3339), "/repo"),
		userMsg(future.Format(time.RFC3339), "later"),
	)
	rows, rep := List(Options{
		Targets: []Target{{Label: Live, Dir: root}},
		Until:   time.Now(),
	})
	if len(rows) != 0 {
		t.Errorf("got %d row(s), want none inside the window", len(rows))
	}
	if rep.Read != 0 {
		t.Errorf("read %d file(s); a shard after the window should be skipped without opening anything", rep.Read)
	}
}

func TestTheLiveCopyIsPreferredAndBothStoresAreNamed(t *testing.T) {
	live, repo := t.TempDir(), t.TempDir()
	at := time.Now().Add(-time.Hour).Format(time.RFC3339)
	shard := time.Now().Format("2006/01/02")
	// The repo's copy is shorter — the live one has been appended to since.
	rolloutFile(t, repo, shard, uuidOld, meta(uuidOld, at, "/repo"))
	rolloutFile(t, live, shard, uuidOld, meta(uuidOld, at, "/repo"), userMsg(at, "and more since"))

	rows, _ := List(Options{Targets: []Target{
		{Label: Live, Dir: live}, {Label: Repo, Dir: repo},
	}})
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want one session found in two places", len(rows))
	}
	r := rows[0]
	if len(r.Stores) != 2 {
		t.Errorf("Stores = %v, want both named", r.Stores)
	}
	if !r.Resumable {
		t.Error("a session in the live home is resumable and should say so")
	}
	// The live copy is at least as complete, and it is the one `codex resume`
	// opens, so it must be the one that gets read.
	if filepath.Dir(r.Path) != filepath.Join(live, filepath.FromSlash("sessions/"+shard)) {
		t.Errorf("read from %q, want the live copy", r.Path)
	}
}

func TestARepoOnlySessionIsNotResumable(t *testing.T) {
	repo := t.TempDir()
	at := time.Now().Format(time.RFC3339)
	rolloutFile(t, repo, time.Now().Format("2006/01/02"), uuidOld, meta(uuidOld, at, "/repo"), userMsg(at, "hello"))
	rows, _ := List(Options{Targets: []Target{{Label: Repo, Dir: repo}}})
	if len(rows) != 1 {
		t.Fatalf("got %d rows", len(rows))
	}
	if rows[0].Resumable {
		t.Error("a session that exists only in the backup cannot be resumed until it is restored")
	}
}

func TestContentSearchOpensBodiesOnlyWhenTheFieldsMiss(t *testing.T) {
	root := t.TempDir()
	at := time.Now().Format(time.RFC3339)
	shard := time.Now().Format("2006/01/02")
	rolloutFile(t, root, shard, uuidOld, meta(uuidOld, at, "/repo"), userMsg(at, "the launcher needs review"))
	rolloutFile(t, root, shard, uuidNew, meta(uuidNew, at, "/other"), userMsg(at, "something unrelated"))

	rows, _ := List(Options{
		Targets: []Target{{Label: Live, Dir: root}},
		Content: "launcher",
	})
	if len(rows) != 1 || rows[0].ID != uuidOld {
		t.Fatalf("got %+v, want only the session whose conversation mentions it", rows)
	}
	// A title match needs no body scan, so no match count is recorded for it.
	rows, _ = List(Options{
		Targets: []Target{{Label: Live, Dir: root}},
		Content: "unrelated",
	})
	if len(rows) != 1 || rows[0].ID != uuidNew {
		t.Fatalf("got %+v", rows)
	}
}

func TestCwdFilter(t *testing.T) {
	root := t.TempDir()
	at := time.Now().Format(time.RFC3339)
	shard := time.Now().Format("2006/01/02")
	rolloutFile(t, root, shard, uuidOld, meta(uuidOld, at, "/Users/x/Git/thing"), userMsg(at, "a"))
	rolloutFile(t, root, shard, uuidNew, meta(uuidNew, at, "/Users/x/Git/other"), userMsg(at, "b"))

	rows, _ := List(Options{Targets: []Target{{Label: Live, Dir: root}}, Cwd: "THING"})
	if len(rows) != 1 || rows[0].ID != uuidOld {
		t.Fatalf("got %+v, want the one whose directory matches, case-insensitively", rows)
	}
}

// The snippet window used the byte offset of the hit in the RAW text, then
// collapsed whitespace and cut the window there — so any message with a run of
// spaces or a newline before the hit centred the window on the wrong place,
// and the snippet could omit the very match it reported.
func TestTheSnippetContainsTheMatchItReports(t *testing.T) {
	root := t.TempDir()
	at := time.Now().Format(time.RFC3339)
	shard := time.Now().Format("2006/01/02")
	padding := strings.Repeat("filler word ", 40) + strings.Repeat("        ", 30) // runs of spaces: valid inside the JSON, gone after Fields
	rolloutFile(t, root, shard, uuidOld, meta(uuidOld, at, "/repo"), userMsg(at, padding+"the NEEDLE sits here"+strings.Repeat(" tail", 60)))
	rows, _ := List(Options{Targets: []Target{{Label: Live, Dir: root}}, Content: "needle"})
	if len(rows) != 1 {
		t.Fatalf("rows = %+v", rows)
	}
	if !strings.Contains(strings.ToLower(rows[0].Snippet), "needle") {
		t.Errorf("snippet does not contain the match: %q", rows[0].Snippet)
	}
}

// Past the chunking threshold a repo-side rollout is an index, and the words
// are in its parts. os.Open read the index, so a body-only match in a large
// conversation was silently missed.
func TestContentSearchReadsAChunkedRolloutsBody(t *testing.T) {
	repo := t.TempDir()
	at := time.Now().Format(time.RFC3339)
	shard := time.Now().Format("2006/01/02")
	path := rolloutFile(t, repo, shard, uuidOld, meta(uuidOld, at, "/repo"), userMsg(at, "an ordinary start"))
	var b strings.Builder
	b.WriteString(meta(uuidOld, at, "/repo") + "\n")
	b.WriteString(userMsg(at, "an ordinary start") + "\n")
	filler := strings.Repeat("y", 900)
	for i := 0; i < 10000; i++ {
		b.WriteString(userMsg(at, filler) + "\n")
	}
	b.WriteString(userMsg(at, "the xylophone appears only at the end") + "\n")
	if err := rolloutstore.Write(path, strings.NewReader(b.String()), time.Now()); err != nil {
		t.Fatal(err)
	}
	rows, _ := List(Options{Targets: []Target{{Label: Repo, Dir: repo}}, Content: "xylophone"})
	if len(rows) != 1 || rows[0].ID != uuidOld {
		t.Fatalf("got %+v, want the chunked session whose body holds the word", rows)
	}
}

// A needle with repeated whitespace counts as a match against the raw text and
// then, uncollapsed, cannot be found in the collapsed window — so the snippet
// showed the start of the message instead of the hit.
func TestAWhitespaceBearingNeedleStillLandsTheSnippet(t *testing.T) {
	root := t.TempDir()
	at := time.Now().Format(time.RFC3339)
	shard := time.Now().Format("2006/01/02")
	padding := strings.Repeat("filler word ", 60)
	rolloutFile(t, root, shard, uuidOld, meta(uuidOld, at, "/repo"), userMsg(at, padding+"the  needle  sits here"+strings.Repeat(" tail", 60)))
	rows, _ := List(Options{Targets: []Target{{Label: Live, Dir: root}}, Content: "needle  sits"})
	if len(rows) != 1 {
		t.Fatalf("rows = %+v", rows)
	}
	if !strings.Contains(rows[0].Snippet, "needle sits") {
		t.Errorf("snippet missed the hit: %q", rows[0].Snippet)
	}
}
