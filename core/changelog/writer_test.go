// Ported from net-changesets Version/ChangelogFileWriterTests.cs.
package changelog

import (
	"os"
	"path/filepath"
	"strings"
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

// TestWriteEntryPreservesNewlinelessFile pins the fix where an existing non-empty
// file with NO trailing newline (a single line, e.g. "# Pkg") had its original
// content discarded. The result must contain both the original title and the new
// entry.
func TestWriteEntryPreservesNewlinelessFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "CHANGELOG.md"), []byte("# Pkg"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteEntry(dir, "Pkg", entryV2); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "CHANGELOG.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "# Pkg") {
		t.Errorf("original title discarded: %q", got)
	}
	if !strings.Contains(string(got), entryV2) {
		t.Errorf("new entry missing: %q", got)
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

// One file, a section per package: a new package appends its section, an
// existing one takes the entry directly under its title, and the first write
// reads exactly like a single-package changelog.
func TestWriteSection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "CHANGELOG.md")
	read := func() string {
		t.Helper()
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(got)
	}
	if err := WriteSection(path, "pkg-a", entryV2); err != nil {
		t.Fatal(err)
	}
	if got := read(); got != "# pkg-a\n\n"+entryV2 {
		t.Fatalf("first section:\n%s", got)
	}
	entryB := "## 0.2.0\n### Minor Changes\n\n- B grew\n"
	if err := WriteSection(path, "pkg-b", entryB); err != nil {
		t.Fatal(err)
	}
	if got := read(); got != "# pkg-a\n\n"+entryV2+"\n# pkg-b\n\n"+entryB {
		t.Fatalf("appended section:\n%s", got)
	}
	entryA3 := "## 3.0.0\n### Major Changes\n\n- Again\n"
	if err := WriteSection(path, "pkg-a", entryA3); err != nil {
		t.Fatal(err)
	}
	if got := read(); got != "# pkg-a\n\n"+entryA3+"\n"+entryV2+"\n# pkg-b\n\n"+entryB {
		t.Fatalf("newest on top within its section:\n%s", got)
	}
	// A title that merely contains another's name is not that section.
	if err := WriteSection(path, "pkg", "## 1.0.0\n\n- first\n"); err != nil {
		t.Fatal(err)
	}
	if got := read(); !strings.HasSuffix(got, "\n# pkg\n\n## 1.0.0\n\n- first\n") {
		t.Fatalf("prefix-named package got its own section:\n%s", got)
	}
}

// A changelog with CRLF endings keeps them, and its sections are still found:
// the second release of a package lands under its title, not in a duplicate
// section at the end.
func TestWriteSectionKeepsCRLF(t *testing.T) {
	path := filepath.Join(t.TempDir(), "CHANGELOG.md")
	if err := os.WriteFile(path, []byte("# pkg-a\r\n\r\n## 1.0.0\r\n\r\n- first\r\n\r\n# pkg-b\r\n\r\n## 0.1.0\r\n\r\n- b\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteSection(path, "pkg-a", "## 1.1.0\n\n- second\n"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "# pkg-a\r\n\r\n## 1.1.0\r\n\r\n- second\r\n\r\n## 1.0.0\r\n\r\n- first\r\n\r\n# pkg-b\r\n\r\n## 0.1.0\r\n\r\n- b\r\n"
	if string(got) != want {
		t.Fatalf("CRLF changelog:\n%q\nwant:\n%q", got, want)
	}
}

// A changelog that is there but cannot be read is never overwritten as if it
// were empty.
func TestWriteSectionRefusesAnUnreadableFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "CHANGELOG.md")
	if err := os.MkdirAll(path, 0o755); err != nil { // a directory where the file should be
		t.Fatal(err)
	}
	if err := WriteSection(path, "pkg", "## 1.0.0\n\n- x\n"); err == nil {
		t.Fatal("an unreadable changelog should be an error, not replaced")
	}
}
