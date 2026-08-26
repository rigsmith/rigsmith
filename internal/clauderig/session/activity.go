package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"time"
)

// A transcript's mtime is not when the session was worked on. Copying restores
// it (clauderig's own restore rewrites every file it lands), a git checkout of
// the synced repo stamps checkout time, and a backup tool touching the tree
// bumps hundreds of files to the same instant — after which "most recent chat"
// means "most recently copied", which is not a question anyone asks. The Desktop
// sidecar's lastActivityAt is no better: it is rebuilt from the same files, so it
// drifts the same way, and it lags a session that is still running.
//
// The records themselves carry the truth. Every Claude Code transcript line is a
// JSON object with its own RFC3339 `timestamp`, written when the record was
// appended, and it survives every copy, sync and restore because it is content
// rather than metadata. LastActivity reads it.

const (
	// tailChunkBytes is the first read window. A session's closing records are a
	// few hundred bytes each, so 64 KiB normally holds dozens of them — but a
	// single record can be enormous (an inlined image), which is why the window
	// doubles rather than being a fixed guess.
	tailChunkBytes = 64 << 10
	// maxTailBytes caps the growth. Past a megabyte with no parseable timestamped
	// record, the file is not a transcript we understand, and the caller's mtime
	// fallback is the honest answer.
	maxTailBytes = 1 << 20
)

// Activity is what a transcript's own records say about the session: when it was
// last appended to, and the working directory and git branch it was on at the
// end. Cwd and GitBranch come from the tail rather than the header on purpose —
// a long session can move, and the last thing it was doing is what makes it
// recognisable in a list.
type Activity struct {
	At        time.Time
	Cwd       string
	GitBranch string
	// Entrypoint is the client that wrote the last record — "claude-vscode",
	// "claude-desktop", "cli", "sdk-*". It answers a question nothing else in the
	// store can: which app a session belongs to. Storage location cannot stand in
	// for it, because every client writes to the same ~/.claude/projects tree.
	//
	// From the LAST record, like Cwd and GitBranch: a session resumed in another
	// client is best described by where you left it, not where it began.
	Entrypoint string
}

// activityLine is the slice of a transcript record this file reads.
type activityLine struct {
	Timestamp  string `json:"timestamp"`
	Cwd        string `json:"cwd"`
	GitBranch  string `json:"gitBranch"`
	Entrypoint string `json:"entrypoint"`
}

// LastActivity reports when a transcript was last written to, according to the
// transcript. It reads only the tail — the common case is one 64 KiB read
// regardless of how large the file is — and returns false for a file with no
// parseable timestamped record within maxTailBytes of the end, leaving the
// caller to fall back.
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
		// record cut in half. Dropping it costs nothing (a later line answers) and
		// keeps a half-record from being parsed as though it were whole.
		if off > 0 {
			if i := bytes.IndexByte(buf, '\n'); i >= 0 {
				buf = buf[i+1:]
			} else {
				buf = nil // one record spans the whole window — grow
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

// latestIn returns the newest timestamp among the complete records in buf, plus
// the cwd/branch/entrypoint from the last record that carries each.
//
// It takes the maximum rather than simply the final record: sub-agent records are
// interleaved into the same file, so the last LINE is not guaranteed to be the
// last MOMENT. Over a tail window that difference is seconds, but taking the max
// costs one comparison per line and removes the caveat entirely.
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
			continue // a record we can't read is not a reason to abandon the file
		}
		// Scanning backwards, so the first cwd/branch seen is the latest one.
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
