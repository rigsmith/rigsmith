package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"time"
)

// Session recency comes from the records, not from the file. A restore, a
// checkout of the synced repo, or any tool that walks the tree rewrites mtime,
// and the Desktop sidecar's lastActivityAt is rebuilt from those same files.
// Record timestamps are content, so they survive the copying.

const (
	// The window doubles rather than being fixed because a single record can be
	// enormous (an inlined image), and stops at maxTailBytes because past that
	// this is not a transcript we understand.
	tailChunkBytes = 64 << 10
	maxTailBytes   = 1 << 20
)

// Activity describes a session from its own records. Taken from the END of the
// transcript rather than the header: a long session can move, and what it was
// last doing is what makes it recognisable.
type Activity struct {
	At        time.Time
	Cwd       string
	GitBranch string
	// "claude-vscode", "claude-desktop", "cli", "sdk-*". Nothing else identifies
	// the client: they all write to the same ~/.claude/projects tree.
	Entrypoint string
}

// activityLine is the slice of a transcript record this file reads.
type activityLine struct {
	Timestamp  string `json:"timestamp"`
	Cwd        string `json:"cwd"`
	GitBranch  string `json:"gitBranch"`
	Entrypoint string `json:"entrypoint"`
}

// LastActivity reports what a transcript says about itself, reading only its
// tail. False means nothing datable was found near the end, so callers fall back
// rather than being handed a guess.
func LastActivity(path string) (Activity, bool) {
	f, err := os.Open(path)
	if err != nil {
		return Activity{}, false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return Activity{}, false
	}
	size := info.Size()
	if size == 0 {
		return Activity{}, false
	}

	for want := int64(tailChunkBytes); ; want *= 2 {
		if want > maxTailBytes {
			want = maxTailBytes
		}
		if want > size {
			want = size
		}
		off := size - want
		buf := make([]byte, want)
		if _, rerr := f.ReadAt(buf, off); rerr != nil && !errors.Is(rerr, io.EOF) {
			return Activity{}, false
		}
		// Unless the window reached the start of the file, its first line is a
		// record cut in half.
		if off > 0 {
			if i := bytes.IndexByte(buf, '\n'); i >= 0 {
				buf = buf[i+1:]
			} else {
				buf = nil // one record spans the whole window
			}
		}
		if a, ok := latestIn(buf); ok {
			return a, true
		}
		if want == size || want == maxTailBytes {
			return Activity{}, false
		}
	}
}

// latestIn takes the newest timestamp rather than the final record's: sub-agent
// records interleave into the same file, so the last LINE need not be the last
// MOMENT.
func latestIn(buf []byte) (Activity, bool) {
	var a Activity
	lines := bytes.Split(buf, []byte{'\n'})
	for i := len(lines) - 1; i >= 0; i-- {
		line := bytes.TrimSpace(lines[i])
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var rec activityLine
		if json.Unmarshal(line, &rec) != nil {
			continue // one unreadable record must not abandon the file
		}
		// Scanning backwards, so the first one seen is the latest.
		if a.Cwd == "" && rec.Cwd != "" {
			a.Cwd = rec.Cwd
		}
		if a.GitBranch == "" && rec.GitBranch != "" {
			a.GitBranch = rec.GitBranch
		}
		if a.Entrypoint == "" && rec.Entrypoint != "" {
			a.Entrypoint = rec.Entrypoint
		}
		if rec.Timestamp == "" {
			continue
		}
		t, terr := time.Parse(time.RFC3339, rec.Timestamp)
		if terr != nil {
			continue
		}
		if t = t.UTC(); t.After(a.At) {
			a.At = t
		}
	}
	return a, !a.At.IsZero()
}
