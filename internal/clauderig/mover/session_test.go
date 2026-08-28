package mover

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/internal/clauderig/project"
)

// sessionTree builds a projects dir holding one session filed under filed, whose
// records carry the given cwds in order.
func sessionTree(t *testing.T, filed, id string, cwds []string) string {
	t.Helper()
	projects := t.TempDir()
	dir := filepath.Join(projects, project.Flatten(filed))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, id+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for i, c := range cwds {
		if err := enc.Encode(map[string]any{"type": "user", "cwd": c, "n": i}); err != nil {
			t.Fatal(err)
		}
	}
	return projects
}

func TestMoveSession_RefilesAndRewritesTheRoot(t *testing.T) {
	const id = "abc"
	old, want := "/Users/john/Git", "/Users/john/Git/thing"
	projects := sessionTree(t, old, id, []string{old, old, want, "/Users/john/Git/other"})

	mv, err := MoveSession(projects, id, want, false)
	if err != nil {
		t.Fatal(err)
	}
	if mv.Records != 2 {
		t.Errorf("Records = %d, want 2 (only the two at the old root)", mv.Records)
	}
	if !mv.Moved {
		t.Error("Moved = false, want the transcript re-filed")
	}
	if _, err := os.Stat(mv.NewPath); err != nil {
		t.Fatalf("transcript not at its new home: %v", err)
	}
	if _, err := os.Stat(mv.OldPath); !os.IsNotExist(err) {
		t.Error("the old copy is still there")
	}
	// The new home has to be the slug the CLI will look under.
	if got := filepath.Base(filepath.Dir(mv.NewPath)); got != project.Flatten(want) {
		t.Errorf("filed under %q, want %q", got, project.Flatten(want))
	}

	body, err := os.ReadFile(mv.NewPath)
	if err != nil {
		t.Fatal(err)
	}
	// Records that were deeper name directories that never moved and must be
	// untouched — this is a re-root, not a rebase.
	if !strings.Contains(string(body), `"/Users/john/Git/other"`) {
		t.Error("a deeper cwd was rewritten; it names a directory that still exists")
	}
	if strings.Contains(string(body), `"cwd":"/Users/john/Git",`) ||
		strings.Contains(string(body), `"cwd":"/Users/john/Git"}`) {
		t.Errorf("the old root survived:\n%s", body)
	}
}

// A dry run has to answer the question without touching anything.
func TestMoveSession_DryRunChangesNothing(t *testing.T) {
	const id = "abc"
	old, want := "/a", "/b"
	projects := sessionTree(t, old, id, []string{old, old, old})
	before, err := os.ReadFile(filepath.Join(projects, project.Flatten(old), id+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}

	mv, err := MoveSession(projects, id, want, true)
	if err != nil {
		t.Fatal(err)
	}
	if mv.Records != 3 {
		t.Errorf("Records = %d, want 3 counted", mv.Records)
	}
	after, err := os.ReadFile(mv.OldPath)
	if err != nil {
		t.Fatalf("the transcript moved during a dry run: %v", err)
	}
	if string(before) != string(after) {
		t.Error("a dry run rewrote the transcript")
	}
}

// Overwriting would lose a conversation.
func TestMoveSession_RefusesToClobberAnExistingTranscript(t *testing.T) {
	const id = "abc"
	old, want := "/a", "/b"
	projects := sessionTree(t, old, id, []string{old})
	dest := filepath.Join(projects, project.Flatten(want))
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, id+".jsonl"), []byte("someone else\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := MoveSession(projects, id, want, false); err == nil {
		t.Fatal("clobbered an existing transcript")
	}
	body, _ := os.ReadFile(filepath.Join(dest, id+".jsonl"))
	if string(body) != "someone else\n" {
		t.Error("the existing transcript was modified")
	}
}

func TestMoveSession_Rejects(t *testing.T) {
	projects := sessionTree(t, "/a", "abc", []string{"/a"})
	if _, err := MoveSession(projects, "abc", "relative/path", false); err == nil {
		t.Error("a relative destination was accepted")
	}
	if _, err := MoveSession(projects, "nope", "/b", false); err == nil {
		t.Error("an unknown session id was accepted")
	}
}

// Re-rooting to where it already is is a no-op, not an error.
func TestMoveSession_SameRootIsANoOp(t *testing.T) {
	projects := sessionTree(t, "/a", "abc", []string{"/a", "/a"})
	mv, err := MoveSession(projects, "abc", "/a", false)
	if err != nil {
		t.Fatal(err)
	}
	if mv.Moved || mv.Records != 0 {
		t.Errorf("no-op did work: %+v", mv)
	}
}
