package adapter_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/clauderig/adapter"
	"github.com/rigsmith/rigsmith/internal/clauderig/ledger"
	"github.com/rigsmith/rigsmith/internal/clauderig/session"
)

func TestSummaryRecordingPreservesNativeRows(t *testing.T) {
	nativeDir, sharedDir := t.TempDir(), t.TempDir()
	at := time.Unix(1700000000, 0).UTC()
	previous := ledger.Entry{ID: "opaque-session", Slug: "project", Title: "original", End: at, Seen: at, Bytes: 10,
		Account: "opaque-account", AccountSource: ledger.AccountFromDesktop, Extra: map[string]json.RawMessage{"future": json.RawMessage(`{"keep":true}`)}}
	next := ledger.Entry{ID: previous.ID, Slug: "project", Title: "updated", Cwd: "/work/project", End: at.Add(time.Hour), Seen: at.Add(time.Hour), Bytes: 20,
		Account: "weaker-inference", AccountSource: ledger.AccountFromSync}
	for _, dir := range []string{nativeDir, sharedDir} {
		l, err := ledger.Open(dir, "fixture")
		if err != nil {
			t.Fatal(err)
		}
		l.Note(previous)
		if err := l.Save(); err != nil {
			t.Fatal(err)
		}
	}
	native, err := ledger.Open(nativeDir, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	shared, err := ledger.Open(sharedDir, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	native.Note(next)
	if !(adapter.LedgerStore{Ledger: shared}).Note(adapter.LedgerSummary(next)) {
		t.Fatal("summary did not update row")
	}
	if err := native.Save(); err != nil {
		t.Fatal(err)
	}
	if err := shared.Save(); err != nil {
		t.Fatal(err)
	}
	read := func(dir string) []byte {
		b, err := os.ReadFile(filepath.Join(dir, "index", "fixture.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	if !bytes.Equal(read(nativeDir), read(sharedDir)) {
		t.Fatal("native serialized bytes differ through summary boundary")
	}
	got := ledger.LoadAll(sharedDir)[previous.ID]
	if got.Account != previous.Account || got.AccountSince != previous.Seen || !bytes.Equal(got.Extra["future"], previous.Extra["future"]) {
		t.Fatalf("lost native attribution or unknown field: %+v", got)
	}
}

func TestMetadataSummaryKeepsProfileAndAccountDistinct(t *testing.T) {
	m := session.Meta{ID: "id", Title: "title", Cwd: "/work/project", LastActivity: time.Unix(1700000000, 0), Account: "account", Profile: "work", Sources: []string{"desktop@work"}}
	s := adapter.MetadataSummary(m)
	if s.Vendor != "claude" || s.ID != m.ID || s.Cwd != m.Cwd || s.Activity != m.LastActivity || !s.Approximate || s.Account != m.Account || s.Profile != m.Profile {
		t.Fatalf("summary: %+v", s)
	}
	s.Sources[0] = "changed"
	if m.Sources[0] != "desktop@work" {
		t.Fatal("summary aliases native source metadata")
	}
}
