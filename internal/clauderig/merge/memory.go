package merge

import (
	"fmt"
	"regexp"
	"strings"
)

// entryRE matches a MEMORY.md index line and captures the memory file it points
// at:  - [Some title](some-memory.md) — one-line hook
//
// The link target is the key: titles and hooks get reworded, filenames don't.
var entryRE = regexp.MustCompile(`^\s*[-*]\s+\[[^\]]*\]\(([^)]+)\)`)

// mergeMemory unions the two machines' memory indexes, keyed by memory
// filename.
//
// Both machines append to MEMORY.md independently, so the file conflicts almost
// every time they diverge, and it is the one file where losing a side actually
// loses knowledge: a dropped line orphans a memory file that still exists on
// disk but is no longer indexed, so it never gets recalled again.
//
// Entries present on both sides keep OUR text — a reworded hook is not worth a
// conflict, and the local machine's phrasing is the one its user last saw.
// Entries only the remote has are spliced in after the entry that preceded them
// there, so related memories stay adjacent instead of piling up at the bottom.
func mergeMemory(s Sides) ([]byte, string, error) {
	ourLines := strings.Split(normalizeNewlines(string(s.Ours)), "\n")
	theirLines := strings.Split(normalizeNewlines(string(s.Theirs)), "\n")

	out := append([]string(nil), ourLines...)
	have := map[string]bool{}
	for _, l := range ourLines {
		if k, ok := entryKey(l); ok {
			have[k] = true
		}
	}

	var added int
	// prevKey tracks the entry preceding the current one on the remote side, so
	// a new entry can be anchored next to the one it followed there.
	prevKey := ""
	for _, l := range theirLines {
		key, ok := entryKey(l)
		if !ok {
			continue
		}
		if have[key] {
			prevKey = key
			continue
		}
		out = spliceAfter(out, prevKey, l)
		have[key] = true
		added++
		prevKey = key
	}

	body := strings.Join(out, "\n")
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	return []byte(body), fmt.Sprintf("%d entry(ies) kept, %d added from remote", len(have)-added, added), nil
}

// spliceAfter inserts line directly after the entry keyed anchor, or appends it
// at the end when that anchor isn't present locally (or there is none, because
// the new entry was first on the remote side).
func spliceAfter(lines []string, anchor, line string) []string {
	if anchor != "" {
		for i, l := range lines {
			if k, ok := entryKey(l); ok && k == anchor {
				out := make([]string, 0, len(lines)+1)
				out = append(out, lines[:i+1]...)
				out = append(out, line)
				return append(out, lines[i+1:]...)
			}
		}
	}
	// No anchor: put it after the last entry rather than at the very end, so it
	// doesn't land below whatever trailing prose or blank lines the file has.
	last := -1
	for i, l := range lines {
		if _, ok := entryKey(l); ok {
			last = i
		}
	}
	if last < 0 {
		return append(lines, line)
	}
	out := make([]string, 0, len(lines)+1)
	out = append(out, lines[:last+1]...)
	out = append(out, line)
	return append(out, lines[last+1:]...)
}

// entryKey returns the memory filename an index line points at.
func entryKey(line string) (string, bool) {
	m := entryRE.FindStringSubmatch(line)
	if m == nil {
		return "", false
	}
	return strings.TrimSpace(m[1]), true
}

func normalizeNewlines(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.TrimSuffix(s, "\n")
}
