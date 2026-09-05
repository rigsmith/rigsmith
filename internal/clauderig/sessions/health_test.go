package sessions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// record is one transcript line carrying a uuid, which is what Describe compares
// copies by.
func record(uuid, ts string) string {
	return `{"type":"user","uuid":"` + uuid + `","timestamp":"` + ts + `"}` + "\n"
}

// splitOf writes a session filed under two project directories and returns the
// Split naming them.
func splitOf(t *testing.T, keepBody, otherBody string) (Split, string) {
	t.Helper()
	live := t.TempDir()
	keep := write(t, live, "projects/-keep/abc.jsonl", keepBody)
	other := write(t, live, "projects/-other/abc.jsonl", otherBody)
	return Split{ID: "abc", Keep: keep, Others: []string{other}}, live
}

// The whole fault being repaired is a conversation that appeared to vanish, so
// the copies are moved and never deleted — and a copy holding records the kept
// one does not is not something to resolve automatically at all.
func TestConsolidate_RefusesWhenTheCopiesHaveDiverged(t *testing.T) {
	s, _ := splitOf(t,
		record("u1", "2026-08-20T09:00:00Z"),
		record("u1", "2026-08-20T09:00:00Z")+record("u2", "2026-08-20T09:01:00Z"))

	park := t.TempDir()
	parked, err := Consolidate(s, park)
	if err == nil {
		t.Fatal("consolidated copies that had diverged")
	}
	if len(parked) != 0 {
		t.Errorf("parked %v despite refusing", parked)
	}
	if _, serr := os.Stat(s.Others[0]); serr != nil {
		t.Error("the diverged copy was moved anyway")
	}
}

// The ordinary case: a copy that is a strict subset is parked, not deleted, and
// under a name that says where it came from.
func TestConsolidate_ParksTheRedundantCopy(t *testing.T) {
	body := record("u1", "2026-08-20T09:00:00Z")
	s, _ := splitOf(t, body+record("u2", "2026-08-20T09:01:00Z"), body)

	park := t.TempDir()
	parked, err := Consolidate(s, park)
	if err != nil {
		t.Fatal(err)
	}
	if len(parked) != 1 {
		t.Fatalf("parked %v, want the one redundant copy", parked)
	}
	if !strings.Contains(filepath.Base(parked[0]), "-other") {
		t.Errorf("parked as %q, want a name naming where it came from", filepath.Base(parked[0]))
	}
	if got, rerr := os.ReadFile(parked[0]); rerr != nil || string(got) != body {
		t.Errorf("the parked copy is not the transcript: %q %v", got, rerr)
	}
	if _, serr := os.Stat(s.Others[0]); !os.IsNotExist(serr) {
		t.Error("the copy was left in place as well as parked")
	}
}

// Consolidate moves rather than deletes, so a parked copy is the only one left.
// A later park generating the same name must not land on top of it.
func TestConsolidate_DoesNotOverwriteAnEarlierParkedCopy(t *testing.T) {
	body := record("u1", "2026-08-20T09:00:00Z")
	s, _ := splitOf(t, body+record("u2", "2026-08-20T09:01:00Z"), body)

	park := t.TempDir()
	taken := filepath.Join(park, "-other__abc.jsonl")
	if err := os.WriteFile(taken, []byte("the first parked copy\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	parked, err := Consolidate(s, park)
	if err != nil {
		t.Fatal(err)
	}
	if len(parked) != 1 || parked[0] == taken {
		t.Fatalf("parked %v, want a name that was free", parked)
	}
	got, err := os.ReadFile(taken)
	if err != nil || string(got) != "the first parked copy\n" {
		t.Errorf("the earlier parked copy was destroyed: %q %v", got, err)
	}
}
