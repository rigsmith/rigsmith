package search

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFile is a test helper that creates dir/rel (with parents) holding data.
func writeFile(t *testing.T, dir, rel string, data []byte) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func collect(targets []Target, opts Options) ([]Match, Stats, error) {
	var got []Match
	stats, err := Search(targets, opts, func(m Match) { got = append(got, m) })
	return got, stats, err
}

func TestSearch_FindsAcrossTargetsAndSkipsBinary(t *testing.T) {
	live := t.TempDir()
	repo := t.TempDir()

	writeFile(t, live, "projects/a.jsonl", []byte(`{"content":"make a c# console project"}`+"\n"))
	writeFile(t, live, "settings.json", []byte(`{"theme":"dark"}`+"\n"))
	writeFile(t, repo, "cli/projects/a.jsonl", []byte("first line\nmake a C# console again\nthird\n"))
	// Binary file containing the needle bytes — must be skipped, not matched.
	writeFile(t, live, "cache/blob.bin", []byte("make a c# console\x00binary"))

	targets := []Target{{Label: "cli", Dir: live}, {Label: "repo", Dir: repo}}
	got, stats, err := collect(targets, Options{Query: "c# console"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if stats.Matches != 2 {
		t.Errorf("Matches = %d, want 2 (live + repo, binary skipped)", stats.Matches)
	}
	if stats.FilesSkipped < 1 {
		t.Errorf("FilesSkipped = %d, want >=1 (the binary blob)", stats.FilesSkipped)
	}
	if len(got) != 2 {
		t.Fatalf("emitted %d matches, want 2: %+v", len(got), got)
	}
	// Case-insensitive by default: "C# console" in the repo file must hit.
	if got[1].Target != "repo" || got[1].Line != 2 {
		t.Errorf("second match = %+v, want repo line 2", got[1])
	}
	// The match offsets must bracket the needle within the snippet.
	m := got[0]
	if m.Snippet[m.MatchAt:m.MatchAt+m.MatchLen] != "c# console" {
		t.Errorf("MatchAt/Len point at %q, want %q", m.Snippet[m.MatchAt:m.MatchAt+m.MatchLen], "c# console")
	}
}

func TestSearch_CaseSensitive(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", []byte("Hello World\nhello world\n"))

	got, _, err := collect([]Target{{Label: "x", Dir: dir}}, Options{Query: "hello", CaseSensitive: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Line != 2 {
		t.Fatalf("case-sensitive got %d matches (want 1 on line 2): %+v", len(got), got)
	}
}

func TestSearch_WindowsLongLines(t *testing.T) {
	dir := t.TempDir()
	long := strings.Repeat("x", 500) + "NEEDLE" + strings.Repeat("y", 500)
	writeFile(t, dir, "big.jsonl", []byte(long+"\n"))

	got, _, err := collect([]Target{{Label: "x", Dir: dir}}, Options{Query: "NEEDLE"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d matches, want 1", len(got))
	}
	m := got[0]
	if len(m.Snippet) > snippetMax+2*len("…")+len("NEEDLE") {
		t.Errorf("snippet not windowed: len=%d", len(m.Snippet))
	}
	if !strings.HasPrefix(m.Snippet, "…") || !strings.HasSuffix(m.Snippet, "…") {
		t.Errorf("windowed snippet should be ellipsis-bracketed: %q", m.Snippet)
	}
	if m.Snippet[m.MatchAt:m.MatchAt+m.MatchLen] != "NEEDLE" {
		t.Errorf("offset points at %q, want NEEDLE", m.Snippet[m.MatchAt:m.MatchAt+m.MatchLen])
	}
}

func TestSearch_ChatsOnlyFiltersToTranscripts(t *testing.T) {
	live := t.TempDir()
	repo := t.TempDir()
	// A real chat transcript (live + repo layouts), plus noise that ChatsOnly must skip.
	writeFile(t, live, "projects/-slug/sess.jsonl", []byte(`{"content":"NEEDLE in a chat"}`+"\n"))
	writeFile(t, live, "projects/-slug/subagents/agent-x.jsonl", []byte("NEEDLE in a subagent\n"))
	writeFile(t, live, "file-history/abc/edit@v1", []byte("NEEDLE in a file-history snapshot\n"))
	writeFile(t, live, "settings.json", []byte(`{"x":"NEEDLE"}`+"\n"))
	writeFile(t, repo, "cli/projects/-slug/sess.jsonl", []byte("NEEDLE in synced chat\n"))
	writeFile(t, repo, "cli/settings.json", []byte("NEEDLE config\n"))

	targets := []Target{{Label: "cli", Dir: live}, {Label: "repo", Dir: repo}}

	// ChatsOnly: only the three *.jsonl under projects/ (live sess, live subagent, repo sess).
	got, _, err := collect(targets, Options{Query: "NEEDLE", ChatsOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("ChatsOnly got %d matches, want 3 (transcripts only): %+v", len(got), rels(got))
	}
	for _, m := range got {
		if !strings.HasSuffix(m.Rel, ".jsonl") || !strings.Contains(m.Rel, "projects/") {
			t.Errorf("ChatsOnly leaked non-transcript: %s", m.Rel)
		}
	}

	// Without ChatsOnly, everything text matches (2 files each side + subagent + history).
	all, _, err := collect(targets, Options{Query: "NEEDLE"})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 6 {
		t.Fatalf("all-files got %d matches, want 6: %+v", len(all), rels(all))
	}
}

func rels(ms []Match) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.Target + ":" + m.Rel
	}
	return out
}

func TestSearch_AcceptFiltersMatches(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "t.jsonl", []byte(
		"KEEP this line\n"+
			"DROP this line\n"+
			"KEEP another\n"))

	// Accept only lines that start with KEEP; DROP lines must be neither counted nor emitted.
	got, stats, err := collect([]Target{{Label: "x", Dir: dir}}, Options{
		Query:  "line",
		Accept: func(line string) bool { return strings.HasPrefix(line, "KEEP") },
	})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Matches != 1 || len(got) != 1 {
		t.Fatalf("Accept should keep 1 match (KEEP this line), got %d / %+v", stats.Matches, got)
	}
	if got[0].Line != 1 {
		t.Errorf("kept match on line %d, want 1", got[0].Line)
	}
}

func TestSearch_ProgressCalled(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 200; i++ {
		writeFile(t, dir, filepath.Join("d", "f"+itoa(i)+".txt"), []byte("x\n"))
	}
	calls := 0
	_, _, err := (func() ([]Match, Stats, error) {
		s, e := Search([]Target{{Label: "x", Dir: dir}}, Options{
			Query:    "nomatch",
			Progress: func(Stats) { calls++ },
		}, nil)
		return nil, s, e
	})()
	if err != nil {
		t.Fatal(err)
	}
	if calls == 0 {
		t.Error("Progress was never called over 200 files")
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

func TestSearch_EmptyQueryErrors(t *testing.T) {
	if _, _, err := collect(nil, Options{Query: ""}); err == nil {
		t.Fatal("empty query should error")
	}
}

func TestSearch_AbsentRootSkipped(t *testing.T) {
	_, stats, err := collect([]Target{{Label: "gone", Dir: "/no/such/dir/clauderig-test"}}, Options{Query: "x"})
	if err != nil {
		t.Fatalf("absent root should not error: %v", err)
	}
	if stats.FilesScanned != 0 || stats.Matches != 0 {
		t.Errorf("absent root scanned files: %+v", stats)
	}
}

func TestSearch_SkipsGitDir(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".git/objects/pack/thing", []byte("secret NEEDLE in git\n"))
	writeFile(t, dir, "real.txt", []byte("NEEDLE here\n"))

	got, _, err := collect([]Target{{Label: "repo", Dir: dir}}, Options{Query: "NEEDLE"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !strings.HasSuffix(got[0].Rel, "real.txt") {
		t.Fatalf("expected only real.txt, got %+v", got)
	}
}
