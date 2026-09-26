package scripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The GitHub release's notes are the changelog's entry for the version, so the
// release page says what the changesets say. These pin down what the extractor
// takes: exactly that entry, and a failure rather than blank notes.

const notesChangelog = `# github.com/rigsmith/rigsmith

## 1.3.0

### 🚀 Enhancements

- A new thing.

## 1.2.0

### 🩹 Fixes

- A fix.

## 1.2.0-rc.1

- A candidate.

## 1.1.0

## 1.0.0

- The first one.
`

func releaseNotes(t *testing.T, version string) (string, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "CHANGELOG.md")
	if err := os.WriteFile(path, []byte(notesChangelog), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("sh", "release-notes.sh", version, path).Output()
	return string(out), err
}

func TestReleaseNotesTakeOnlyThatVersionsEntry(t *testing.T) {
	for _, tc := range []struct{ version, want string }{
		{"1.3.0", "### 🚀 Enhancements\n\n- A new thing.\n"},
		{"v1.3.0", "### 🚀 Enhancements\n\n- A new thing.\n"},
		// A ### heading is part of the entry; the next ## ends it, and a
		// prerelease of the same version is a different entry.
		{"1.2.0", "### 🩹 Fixes\n\n- A fix.\n"},
		{"1.2.0-rc.1", "- A candidate.\n"},
		{"1.0.0", "- The first one.\n"},
	} {
		got, err := releaseNotes(t, tc.version)
		if err != nil {
			t.Errorf("%s: %v", tc.version, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s: notes = %q, want %q", tc.version, got, tc.want)
		}
	}
}

func TestReleaseNotesRefuseAMissingOrEmptyEntry(t *testing.T) {
	for _, version := range []string{"9.9.9", "1.1.0", "1.2"} {
		out, err := releaseNotes(t, version)
		if err == nil {
			t.Errorf("%s: printed %q; want a failure, not blank or borrowed notes", version, out)
		}
		if strings.TrimSpace(out) != "" {
			t.Errorf("%s: printed %q on failure; want nothing on stdout", version, out)
		}
	}
}
