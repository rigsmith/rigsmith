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
