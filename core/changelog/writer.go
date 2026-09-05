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
	existing, _ := os.ReadFile(path)
	header := fmt.Sprintf("# %s\n", displayName)
	if len(existing) == 0 {
		return os.WriteFile(path, []byte(header+"\n"+entry), 0o644)
	}
	text := string(existing)
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	at := -1
	if strings.HasPrefix(text, header) {
		at = len(header)
	} else if i := strings.Index(text, "\n"+header); i >= 0 {
		at = i + 1 + len(header)
	}
	if at < 0 {
		return os.WriteFile(path, []byte(text+"\n"+header+"\n"+entry), 0o644)
	}
	return os.WriteFile(path, []byte(text[:at]+"\n"+entry+text[at:]), 0o644)
}
