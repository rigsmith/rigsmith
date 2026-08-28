package sessions

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rigsmith/rigsmith/internal/clauderig/session"
)

const delID = "abcdef01-2345-4678-89ab-cdef01234567"

// rowIn builds a row holding the session in each named store, with real files.
func rowIn(t *testing.T, stores ...string) (Row, map[string]string) {
	t.Helper()
	root := t.TempDir()
	row := Row{ID: delID, Paths: map[string]string{}}
	made := map[string]string{}
	for _, s := range stores {
		p := write(t, root, s+"/projects/-p/"+delID+".jsonl", "{}\n")
		row.Paths[s] = p
		made[s] = p
	}
	return row, made
}

// Deleting one store must leave the others exactly where they were. This is the
// whole reason the choice is per-store.
func TestDelete_OnlyTheNamedStores(t *testing.T) {
	row, made := rowIn(t, CLISource, DesktopSource, RepoSource)

	d, err := Delete(row, []string{CLISource}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Removed) != 1 || d.Removed[0] != made[CLISource] {
		t.Fatalf("Removed = %v, want only the cli copy", d.Removed)
	}
	if _, err := os.Stat(made[CLISource]); !os.IsNotExist(err) {
		t.Error("the cli copy survived the delete")
	}
	for _, s := range []string{DesktopSource, RepoSource} {
		if _, err := os.Stat(made[s]); err != nil {
			t.Errorf("%s copy was removed but was not selected", s)
		}
	}
	// The caller has to be able to say "this is still elsewhere", or the window
	// reports a delete that a restore will quietly undo.
	if len(d.Remaining) != 2 {
		t.Errorf("Remaining = %v, want both unselected stores named", d.Remaining)
	}
}

// Sub-agent transcripts live in a sibling directory named for the session and
// are part of the conversation; leaving them orphans data the user meant to remove.
func TestDelete_TakesSubagentTranscripts(t *testing.T) {
	root := t.TempDir()
	p := write(t, root, "cli/projects/-p/"+delID+".jsonl", "{}\n")
	write(t, root, "cli/projects/-p/"+delID+"/subagents/a.jsonl", "{}\n")
	sibling := write(t, root, "cli/projects/-p/other.jsonl", "{}\n")
	row := Row{ID: delID, Paths: map[string]string{CLISource: p}}

	if _, err := Delete(row, []string{CLISource}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "cli/projects/-p/"+delID)); !os.IsNotExist(err) {
		t.Error("the sub-agent directory survived")
	}
	if _, err := os.Stat(sibling); err != nil {
		t.Error("an unrelated transcript in the same project was removed")
	}
}

// Desktop sidecars are filed per install, so a session listed by two of them has
// two — and both belong to the store they were scanned under.
func TestDelete_RemovesSidecarsOfThatStoreOnly(t *testing.T) {
	root := t.TempDir()
	live := write(t, root, "desk/claude-code-sessions/a/o/local_"+delID+".json", "{}")
	staged := write(t, root, "repo/desktop/claude-code-sessions/a/o/local_"+delID+".json", "{}")
	row := Row{ID: delID, Paths: map[string]string{}, Meta: session.Meta{Sidecars: []session.SidecarRef{
		{Label: DesktopSource, Path: live},
		{Label: RepoSource, Path: staged},
	}}}

	if _, err := Delete(row, []string{DesktopSource}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(live); !os.IsNotExist(err) {
		t.Error("the live sidecar survived")
	}
	if _, err := os.Stat(staged); err != nil {
		t.Error("the staged sidecar was removed but the repo store was not selected")
	}
}

// The name guard is braces for a future change to the path scan: a delete that
// is handed a directory root must refuse rather than succeed.
func TestDelete_RefusesAPathThatIsNotThisSession(t *testing.T) {
	root := t.TempDir()
	other := write(t, root, "cli/projects/-p/something-else.jsonl", "{}\n")
	row := Row{ID: delID, Paths: map[string]string{CLISource: other}}

	d, err := Delete(row, []string{CLISource}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Removed) != 0 {
		t.Fatalf("removed %v — a path not named for the session must be refused", d.Removed)
	}
	if len(d.Failed) != 1 {
		t.Fatalf("Failed = %v, want the refusal reported", d.Failed)
	}
	if _, err := os.Stat(other); err != nil {
		t.Error("an unrelated file was deleted")
	}
}

// Already-gone is the state the caller asked for, not an error to report.
func TestDelete_MissingFileIsNotAFailure(t *testing.T) {
	row := Row{ID: delID, Paths: map[string]string{
		CLISource: filepath.Join(t.TempDir(), delID+".jsonl"),
	}}
	d, err := Delete(row, []string{CLISource}, "")
	if err != nil || len(d.Failed) != 0 {
		t.Errorf("err=%v failed=%v, want a missing file to count as removed", err, d.Failed)
	}
}

// Naming no store at all is a caller bug, and silently doing nothing would look
// like a successful delete.
func TestDelete_NoStoresIsAnError(t *testing.T) {
	if _, err := Delete(Row{ID: delID}, nil, ""); err == nil {
		t.Error("deleting with no stores selected should error")
	}
}
