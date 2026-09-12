package contents

import (
	"bytes"
	"github.com/rigsmith/rigsmith/internal/codexrig/rolloutstore"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func file(t *testing.T, root, rel string, size int) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
}

const rollout1 = "cli/sessions/2026/09/05/rollout-2026-09-05T11-22-59-01a0722a-7356-7592-922a-336289bdc101.jsonl"

func TestCategoriesAreNamedInTheReadersVocabulary(t *testing.T) {
	root := t.TempDir()
	file(t, root, rollout1, 900)
	file(t, root, "cli/config.toml", 40)
	file(t, root, "cli/AGENTS.md", 30)
	file(t, root, "cli/skills/mine/SKILL.md", 20)
	file(t, root, "cli/rules/default.rules", 10)
	file(t, root, "codexrig-manifest.json", 5)
	file(t, root, "index/one.jsonl", 5)

	rep, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int64{
		"sessions":         900,
		"config":           40,
		"instructions":     30,
		"skills":           20,
		"rules":            10,
		"codexrig records": 10,
	}
	got := map[string]int64{}
	for _, g := range rep.Groups {
		got[g.Name] = g.Bytes
	}
	for name, bytes := range want {
		if got[name] != bytes {
			t.Errorf("%s = %d bytes, want %d (groups: %+v)", name, got[name], bytes, rep.Groups)
		}
	}
	// Largest first, because the point is which kind of thing is big.
	if rep.Groups[0].Name != "sessions" {
		t.Errorf("first group = %q, want the largest", rep.Groups[0].Name)
	}
}

func TestGitsOwnStorageIsNotCountedAsContents(t *testing.T) {
	// Counting it would double every byte and answer a different question.
	root := t.TempDir()
	file(t, root, "cli/config.toml", 100)
	file(t, root, ".git/objects/pack/huge.pack", 100000)

	rep, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Bytes != 100 {
		t.Errorf("total = %d, want only the working tree", rep.Bytes)
	}
}

func TestFoldOnlyKicksInWhenItSaysSomething(t *testing.T) {
	// Folding a single small category into "other" renames it and tells the
	// reader nothing.
	one := Report{Bytes: 1000, Groups: []Group{
		{Name: "sessions", Bytes: 990}, {Name: "config", Bytes: 10},
	}}
	if got := one.Fold(); len(got.Groups) != 2 || got.Groups[1].Name != "config" {
		t.Errorf("a single small category was folded: %+v", got.Groups)
	}
	two := Report{Bytes: 1000, Groups: []Group{
		{Name: "sessions", Bytes: 980}, {Name: "config", Bytes: 10}, {Name: "rules", Bytes: 10},
	}}
	got := two.Fold()
	if len(got.Groups) != 2 || got.Groups[1].Name != "other" {
		t.Errorf("two small categories should fold: %+v", got.Groups)
	}
	if got.Groups[1].Bytes != 20 {
		t.Errorf("other = %d bytes, want the sum", got.Groups[1].Bytes)
	}
	if got.Groups[1].Detail == "" {
		t.Error("other should say what it swallowed")
	}
}

func TestAnEmptyRepoScansCleanly(t *testing.T) {
	rep, err := Scan(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Files != 0 || rep.Bytes != 0 || len(rep.Groups) != 0 {
		t.Errorf("rep = %+v, want empty", rep)
	}
	if len(rep.Fold().Groups) != 0 {
		t.Error("folding an empty report invented a group")
	}
}

// A chunked rollout is one conversation. Counting its parts as files and its
// index at a few hundred bytes reported a 172 MB session as a handful of 4 MiB
// config files and one tiny session.
func TestScanCountsAChunkedRolloutOnceAtItsLogicalSize(t *testing.T) {
	dir := t.TempDir()
	rel := "cli/sessions/2026/09/05/rollout-2026-09-05T11-22-59-01a0722a-7356-7592-922a-336289bdc101.jsonl"
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	body := bytes.Repeat([]byte(`{"type":"response_item","payload":{"text":"yyyyyyyy"}}`+"\n"), 200000)
	if err := rolloutstore.Write(p, bytes.NewReader(body), time.Now()); err != nil {
		t.Fatal(err)
	}
	rep, err := Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	var sessions *Group
	for i := range rep.Groups {
		if rep.Groups[i].Name == "sessions" {
			sessions = &rep.Groups[i]
		}
	}
	if sessions == nil {
		t.Fatalf("no sessions group: %+v", rep.Groups)
	}
	if sessions.Files != 1 || sessions.Bytes != int64(len(body)) {
		t.Errorf("sessions = %d files, %d bytes; want 1 file of %d", sessions.Files, sessions.Bytes, len(body))
	}
	if rep.Files != 1 || rep.Bytes != int64(len(body)) {
		t.Errorf("total = %d files, %d bytes; want the conversation counted once", rep.Files, rep.Bytes)
	}
}
