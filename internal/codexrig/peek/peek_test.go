package peek

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/internal/codexrig/rolloutstore"
)

const relRollout = "sessions/2026/09/05/rollout-2026-09-05T11-22-59-01a0722a-7356-7592-922a-336289bdc101.jsonl"

// bigRollout is comfortably past the chunking threshold, with a real header.
func bigRollout(turns int) []byte {
	var b strings.Builder
	b.WriteString(`{"timestamp":"2026-09-05T11:22:59.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"01a0722a-7356-7592-922a-336289bdc101","cwd":"/Users/x/Git/thing","cli_version":"0.144.6"}}` + "\n")
	b.WriteString(`{"timestamp":"2026-09-05T11:23:00.000Z","ordinal":1,"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"review the launcher"}]}}` + "\n")
	filler := strings.Repeat("y", 900)
	for i := 0; i < turns; i++ {
		b.WriteString(`{"timestamp":"2026-09-05T11:24:00.000Z","ordinal":2,"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"` + filler + `"}]}}` + "\n")
	}
	return []byte(b.String())
}

// chunkedRepo commits a chunked rollout and returns the repo and the bytes.
func chunkedRepo(t *testing.T) (*gitrepo.Repo, []byte) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	repo, err := gitrepo.Init(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	want := bigRollout(12000) // ~11 MB
	p := filepath.Join(dir, "cli", filepath.FromSlash(relRollout))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := rolloutstore.Write(p, bytes.NewReader(want), time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := repo.StageAll(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Commit(ctx, "codexrig sync: mbp"); err != nil {
		t.Fatal(err)
	}
	return repo, want
}

// Past the chunking threshold the repo holds an index, and the conversation is
// in the parts it names. Handing the index back as the rollout gave `peek show`
// a one-line JSON document, `peek list` no title, and `peek get` a file Codex
// could not resume.
func TestReadRebuildsAChunkedRolloutFromTheRef(t *testing.T) {
	repo, want := chunkedRepo(t)
	s := Session{Path: "cli/" + relRollout}
	got, err := Read(context.Background(), repo, "main", s)
	if err != nil {
		t.Fatal(err)
	}
	if rolloutstore.IsIndex(got) {
		t.Fatal("Read returned the chunk index instead of the conversation")
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("Read returned %d bytes, want %d, or different content", len(got), len(want))
	}
}

func TestTitlesReadTheHeaderOfAChunkedRollout(t *testing.T) {
	repo, _ := chunkedRepo(t)
	got := Titles(context.Background(), repo, "main", []Session{{Path: "cli/" + relRollout}})
	if got[0].Title != "review the launcher" || got[0].Cwd != "/Users/x/Git/thing" {
		t.Errorf("title=%q cwd=%q — the listing read the index, not the header", got[0].Title, got[0].Cwd)
	}
}

func TestGetWritesAResumableRolloutWithPrivateMode(t *testing.T) {
	repo, want := chunkedRepo(t)
	home := t.TempDir()
	got, err := Get(context.Background(), repo, "main", Session{Path: "cli/" + relRollout}, home)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(got.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b, want) {
		t.Error("peek get wrote something other than the conversation")
	}
	if st, _ := os.Stat(got.Path); st != nil && st.Mode().Perm()&0o077 != 0 && !isWindows() {
		t.Errorf("mode %v: the whole conversation is readable by other local accounts", st.Mode().Perm())
	}
}

// A listing must not show a title from a part that Read would refuse.
func TestTitlesRefuseATamperedFirstPart(t *testing.T) {
	repo, _ := chunkedRepo(t)
	ctx := context.Background()
	raw, err := repo.ShowFile(ctx, "main", "cli/"+relRollout)
	if err != nil {
		t.Fatal(err)
	}
	idx, err := rolloutstore.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	// Rewrite the first part in the working tree with a different, still
	// parseable header, and commit it under the same name.
	partPath := filepath.Join(repo.Dir, filepath.FromSlash(rolloutstore.PartPath("cli/"+relRollout, idx.Parts[0].Hash)))
	body, err := os.ReadFile(partPath)
	if err != nil {
		t.Fatal(err)
	}
	forged := bytes.Replace(body, []byte("review the launcher"), []byte("something forged!!"), 1)
	if err := os.WriteFile(partPath, forged, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := repo.StageAll(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Commit(ctx, "tamper"); err != nil {
		t.Fatal(err)
	}
	got := Titles(ctx, repo, "main", []Session{{Path: "cli/" + relRollout}})
	if got[0].Title != "" || got[0].Cwd != "" {
		t.Errorf("title=%q cwd=%q from a part that does not match its index", got[0].Title, got[0].Cwd)
	}
}
