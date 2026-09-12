package mergepolicy

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/clauderig/ledger"
)

func TestRetainedLedgerUsesNativeReconciliation(t *testing.T) {
	// The newest session snapshot and strongest account attribution can be on
	// different sides. Preserve unknown row fields and let both native readers agree.
	old := []byte("{\"id\":\"s\",\"title\":\"old\",\"end\":\"2026-01-01T00:00:00Z\",\"seen\":\"2026-01-01T00:00:00Z\",\"account\":\"owner\",\"accountSource\":\"desktop\"}\n")
	newer := []byte("{\"id\":\"s\",\"title\":\"new\",\"end\":\"2026-01-02T00:00:00Z\",\"seen\":\"2026-01-03T00:00:00Z\",\"future\":{\"keep\":true}}\n")
	for _, sides := range [][2][]byte{{old, newer}, {newer, old}} {
		merged, err := ResolveRetained(t.Context(), "index/fixture.jsonl", nil, sides[0], sides[1])
		if err != nil || !bytes.Contains(merged, old) || !bytes.Contains(merged, newer) {
			t.Fatalf("rows lost: %q %v", merged, err)
		}
		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, "index"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "index", "fixture.jsonl"), merged, 0600); err != nil {
			t.Fatal(err)
		}
		rows := ledger.LoadAll(dir)
		if rows["s"].Title != "new" || rows["s"].Account != "owner" || string(rows["s"].Extra["future"]) != "{\"keep\":true}" {
			t.Fatalf("native union: %+v", rows["s"])
		}
		opened, err := ledger.Open(dir, "fixture")
		if err != nil {
			t.Fatal(err)
		}
		account, _ := opened.Attribution("s")
		if account != "owner" || !opened.Fresh("s", time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), 0) {
			t.Fatal("Open disagrees with LoadAll")
		}
	}
}

func TestRetainedLedgerRejectsIncompleteRowsAndOtherPaths(t *testing.T) {
	valid := []byte("{\"id\":\"s\"}\n")
	for _, bad := range [][]byte{nil, []byte("{\"id\":\"s\"}"), []byte("{broken}\n"), []byte("[]\n")} {
		if _, err := ResolveRetained(t.Context(), "index/fixture.jsonl", nil, bad, valid); err == nil {
			t.Fatal("accepted incomplete ledger")
		}
	}
	for _, path := range []string{"index/nested/fixture.jsonl", "index/.temporary.jsonl", "other/fixture.jsonl"} {
		if _, err := ResolveRetained(t.Context(), path, nil, valid, valid); err == nil {
			t.Fatal("expanded ledger scope", path)
		}
	}
}
