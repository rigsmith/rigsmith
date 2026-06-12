// Ported from net-changesets Version/ChangelogFileWriterTests.cs.
package changelog

import (
	"os"
	"path/filepath"
	"testing"
)

const entryV2 = "## 2.0.0\n### Major Changes\n\n- Breaking change\n"

func TestWriteEntryGeneratesNewFileWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	if err := WriteEntry(dir, "pkg-a", entryV2); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "CHANGELOG.md"))
	if err != nil {
		t.Fatal(err)
	}
	want := "# pkg-a\n\n" + entryV2
	if string(got) != want {
		t.Errorf("new file:\ngot  %q\nwant %q", got, want)
	}
}

func TestWriteEntryAmendsExistingFileNewestOnTop(t *testing.T) {
	dir := t.TempDir()
	existing := "# pkg-a\n\n## 1.0.0\n### Patch Changes\n\n- Old fix\n"
	if err := os.WriteFile(filepath.Join(dir, "CHANGELOG.md"), []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteEntry(dir, "pkg-a", entryV2); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "CHANGELOG.md"))
	if err != nil {
		t.Fatal(err)
	}
	// The new entry slots in directly under the title; prior history follows.
	want := "# pkg-a\n\n" + entryV2 + "\n## 1.0.0\n### Patch Changes\n\n- Old fix\n"
	if string(got) != want {
		t.Errorf("amended file:\ngot  %q\nwant %q", got, want)
	}
}

func TestWriteEntryGeneratesTwoChangelogsForMultipleProjects(t *testing.T) {
	root := t.TempDir()
	for _, p := range []string{"pkg-a", "pkg-b"} {
		dir := filepath.Join(root, p)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := WriteEntry(dir, p, entryV2); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{"pkg-a", "pkg-b"} {
		got, err := os.ReadFile(filepath.Join(root, p, "CHANGELOG.md"))
		if err != nil {
			t.Fatal(err)
		}
		if want := "# " + p + "\n\n" + entryV2; string(got) != want {
			t.Errorf("%s:\ngot  %q\nwant %q", p, got, want)
		}
	}
}
