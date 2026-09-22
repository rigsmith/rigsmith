// Ported from net-changesets Shared/PreStateRepositoryTests.cs.
package prestate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadNoFileReturnsNil(t *testing.T) {
	ps, err := Read(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if ps != nil {
		t.Errorf("Read on missing pre.json = %+v, want nil", ps)
	}
}

func TestWriteThenReadRoundTrips(t *testing.T) {
	dir := t.TempDir()
	if err := Write(dir, &PreState{Mode: ModePre, Tag: "next"}); err != nil {
		t.Fatal(err)
	}
	if !Has(dir) {
		t.Error("Has should be true after Write")
	}

	out, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if out.Mode != ModePre || out.Tag != "next" {
		t.Errorf("round trip: mode=%q tag=%q", out.Mode, out.Tag)
	}

	// The on-disk shape is @changesets v3's: only mode and tag, two-space
	// indent, trailing newline.
	raw, err := os.ReadFile(filepath.Join(dir, "pre.json"))
	if err != nil {
		t.Fatal(err)
	}
	if want := "{\n  \"mode\": \"pre\",\n  \"tag\": \"next\"\n}\n"; string(raw) != want {
		t.Errorf("pre.json = %q, want %q", raw, want)
	}
}

// A pre.json written by @changesets v2 lists consumed ids (and initial
// versions); it must still read, so a prerelease begun under v2 carries on.
func TestReadsV2State(t *testing.T) {
	dir := t.TempDir()
	v2 := `{ "mode": "pre", "tag": "next", "initialVersions": { "pkg-a": "1.0.0" }, "changesets": ["brave-pandas-smile"] }`
	if err := os.WriteFile(filepath.Join(dir, "pre.json"), []byte(v2), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !out.Contains("brave-pandas-smile") || out.Contains("other") {
		t.Errorf("Contains misreports the v2 consumed list %v", out.Changesets)
	}
}

func TestMoveToPreMovesConsumedAndMigratesV2List(t *testing.T) {
	dir := t.TempDir()
	for _, id := range []string{"old-one", "new-one", "untouched"} {
		if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte("---\n---\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ps := &PreState{Mode: ModePre, Tag: "next", Changesets: []string{"old-one"}}
	// "from-commit" has no file: skipped, not an error.
	undo, err := ps.MoveToPre(dir, []string{"new-one", "from-commit"})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"old-one", "new-one"} {
		if _, err := os.Stat(filepath.Join(dir, "pre", id+".md")); err != nil {
			t.Errorf("%s should be in pre/: %v", id, err)
		}
		if _, err := os.Stat(filepath.Join(dir, id+".md")); err == nil {
			t.Errorf("%s should be gone from the top level", id)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "untouched.md")); err != nil {
		t.Errorf("an unconsumed changeset must stay put: %v", err)
	}
	if ps.Changesets != nil {
		t.Errorf("the v2 list should be cleared once migrated, got %v", ps.Changesets)
	}
	if err := Write(dir, ps); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(filepath.Join(dir, "pre.json")); strings.Contains(string(raw), "changesets") {
		t.Errorf("pre.json should not carry a changesets list after migration:\n%s", raw)
	}

	// A caller whose later write fails puts everything back.
	undo()
	for _, id := range []string{"old-one", "new-one"} {
		if _, err := os.Stat(filepath.Join(dir, id+".md")); err != nil {
			t.Errorf("undo should return %s to the top level: %v", id, err)
		}
	}
	if len(ps.Changesets) != 1 || ps.Changesets[0] != "old-one" {
		t.Errorf("undo should restore the v2 list, got %v", ps.Changesets)
	}
}

func TestMoveToPreIsAllOrNothing(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "first.md"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	// A directory where "second" would land makes that rename fail.
	if err := os.WriteFile(filepath.Join(dir, "second.md"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "pre", "second.md", "blocker"), 0o755); err != nil {
		t.Fatal(err)
	}
	ps := &PreState{Mode: ModePre, Tag: "next"}
	if _, err := ps.MoveToPre(dir, []string{"first", "second"}); err == nil {
		t.Fatal("MoveToPre should fail when a file cannot be moved")
	}
	if _, err := os.Stat(filepath.Join(dir, "first.md")); err != nil {
		t.Errorf("a failed MoveToPre must put first.md back: %v", err)
	}
}

func TestReturnToTopMovesLeftovers(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "pre"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pre", "kept.md"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ReturnToTop(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "kept.md")); err != nil {
		t.Errorf("kept.md should be back at the top level: %v", err)
	}
	if err := ReturnToTop(t.TempDir()); err != nil {
		t.Errorf("no pre/ directory is not an error: %v", err)
	}
}

func TestRemoveDirKeepsANonEmptyDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "pre"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pre", "kept.md"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	RemoveDir(dir)
	if _, err := os.Stat(filepath.Join(dir, "pre", "kept.md")); err != nil {
		t.Errorf("a changeset left in pre/ must survive: %v", err)
	}
	_ = os.Remove(filepath.Join(dir, "pre", "kept.md"))
	RemoveDir(dir)
	if _, err := os.Stat(filepath.Join(dir, "pre")); err == nil {
		t.Error("an empty pre/ should be removed")
	}
}

func TestRemoveDeletesFile(t *testing.T) {
	dir := t.TempDir()
	if err := Write(dir, &PreState{Mode: ModePre, Tag: "next"}); err != nil {
		t.Fatal(err)
	}
	if err := Remove(dir); err != nil {
		t.Fatal(err)
	}
	if Has(dir) {
		t.Error("pre.json should be gone after Remove")
	}
	// Removing again is a no-op, not an error.
	if err := Remove(dir); err != nil {
		t.Errorf("Remove on absent file = %v, want nil", err)
	}
}
