package changelog

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// FileName is the conventional changelog file name within a package directory.
const FileName = "CHANGELOG.md"

// WriteEntry prepends a pre-rendered release entry to <dir>/CHANGELOG.md,
// creating the file with a `# DisplayName` title when absent (the engine owns
// file placement; the generator only rendered the entry — per the changelog
// plugin contract). Newest release sits on top, directly under the title.
// Ported from net-changesets' ChangelogFileWriter; the byte layout is pinned by
// the parity-corpus goldens.
func WriteEntry(dir, displayName, entry string) error {
	path := filepath.Join(dir, FileName)

	existing, _ := os.ReadFile(path)
	header := fmt.Sprintf("# %s\n", displayName)

	var body string
	if len(existing) == 0 {
		body = header + "\n" + entry
	} else {
		text := string(existing)
		if nl := strings.IndexByte(text, '\n'); nl >= 0 {
			body = text[:nl+1] + "\n" + entry + text[nl+1:]
		} else {
			// Non-empty but newline-free: a single line (e.g. an existing title
			// with no trailing newline). Treat it as the first line and insert the
			// entry after it — the old code discarded this content entirely.
			body = text + "\n\n" + entry
		}
	}
	return os.WriteFile(path, []byte(body), 0o644)
}

// WriteSection prepends a pre-rendered release entry under the `# DisplayName`
// title inside one shared changelog file at path — a file that holds a section
// per package, for packages that cannot carry a CHANGELOG.md of their own. A
// member of a stackspace is the case: its directory belongs to its upstream,
// so its notes go to the stackspace root instead, one section per member.
//
// The section is found by its exact title line. When present, the entry slots
// in directly under it (newest on top, as WriteEntry does); when absent, a new
// section is appended at the end. A file that does not exist yet starts with
// this one section, so a single-package file reads exactly as WriteEntry would
// have written it.
func WriteSection(path, displayName, entry string) error {
	// A file that is there but cannot be read is not an empty one: writing
	// "the first section" over it would discard every release before.
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	header := fmt.Sprintf("# %s\n", displayName)
	entry = strings.ReplaceAll(entry, "\r\n", "\n")
	if len(existing) == 0 {
		return os.WriteFile(path, []byte(header+"\n"+entry), 0o644)
	}
	// The section is found on LF text, whatever the file uses, and the file
	// keeps its own endings: a CRLF changelog would otherwise never match its
	// own "# Name" line and grow a duplicate section per release.
	text := string(existing)
	crlf := strings.Contains(text, "\r\n")
	if crlf {
		text = strings.ReplaceAll(text, "\r\n", "\n")
	}
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	// The title is matched as a whole line — at the start of the file or
	// after a newline, up to and including its own — so "# pkg" is never
	// found inside "# pkg-docs".
	at := -1
	if strings.HasPrefix(text, header) {
		at = len(header)
	} else if i := strings.Index(text, "\n"+header); i >= 0 {
		at = i + 1 + len(header)
	}
	var body string
	if at < 0 {
		body = text + "\n" + header + "\n" + entry
	} else {
		body = text[:at] + "\n" + entry + text[at:]
	}
	if crlf {
		body = strings.ReplaceAll(body, "\n", "\r\n")
	}
	return os.WriteFile(path, []byte(body), 0o644)
}
