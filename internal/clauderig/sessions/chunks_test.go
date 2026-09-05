package sessions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/clauderig/transcript"
)

func TestDeleteChunkedSessionRemovesItsParts(t *testing.T) {
	for _, ext := range []string{".jsonl", ".JSONL"} {
		p := filepath.Join(t.TempDir(), delID+ext)
		if err := transcript.Write(p, strings.NewReader("body\n"), time.Now()); err != nil {
			t.Fatal(err)
		}
		if err := removeSessionPath(p, delID); err != nil {
			t.Fatal(err)
		}
		for _, path := range []string{p, p + transcript.Suffix} {
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("left behind %s", path)
			}
		}
	}

}

func TestConsolidateParksChunkedCopyAsNative(t *testing.T) {
	body := record("u1", "2026-08-20T09:00:00Z")
	s, _ := splitOf(t, body+record("u2", "2026-08-20T09:01:00Z"), body)
	if err := transcript.Write(s.Others[0], strings.NewReader(body), time.Now()); err != nil {
		t.Fatal(err)
	}
	if !Describe(s).Safe {
		t.Fatal("chunked subset not recognized")
	}
	parked, err := Consolidate(s, t.TempDir())
	if err != nil || len(parked) != 1 {
		t.Fatalf("park: %v %v", parked, err)
	}
	got, err := transcript.ReadFile(parked[0])
	if err != nil || string(got) != body {
		t.Fatalf("parked copy unreadable: %v", err)
	}
	raw, err := os.ReadFile(parked[0])
	if err != nil || transcript.IsIndex(raw) || string(raw) != body {
		t.Fatal("parked copy is not self-contained")
	}
	for _, p := range []string{s.Others[0], s.Others[0] + transcript.Suffix} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("original left behind: %v", err)
		}
	}
}

func TestConsolidateCorruptChunksLeavesSourceIntact(t *testing.T) {
	body := record("u1", "2026-08-20T09:00:00Z")
	s, _ := splitOf(t, body, body)
	src := s.Others[0]
	if err := transcript.Write(src, strings.NewReader(body), time.Now()); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	idx, err := transcript.Decode(before)
	if err != nil {
		t.Fatal(err)
	}
	part := filepath.Join(src+transcript.Suffix, idx.Parts[0].Hash+".part")
	if err := os.WriteFile(part, []byte("corrupt"), 0o644); err != nil {
		t.Fatal(err)
	}
	park := t.TempDir()
	if paths, err := Consolidate(s, park); err == nil || len(paths) != 0 {
		t.Fatalf("corrupt copy parked: %v %v", paths, err)
	}
	after, err := os.ReadFile(src)
	if err != nil || string(after) != string(before) {
		t.Fatalf("source index lost: %v", err)
	}
	if _, err := os.Stat(part); err != nil {
		t.Fatalf("source parts removed: %v", err)
	}
	files, err := os.ReadDir(park)
	if err != nil || len(files) != 0 {
		t.Fatalf("partial parked copy left behind: %v", err)
	}
}
