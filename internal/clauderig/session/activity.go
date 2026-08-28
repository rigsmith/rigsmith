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
	// LastPrompt is the most recent thing a person actually typed, flattened to
	// one line. Distinct from the title, which is the FIRST prompt: what a
	// session was opened to do and what it was last asked to do are rarely the
	// same after an hour, and the second is what makes a row recognisable when
	// you are looking for the chat you were just in.
	//
	// Empty when the tail window held no typed prompt. A session that ends in a
	// long run of tool calls can push the last human turn out of the window, and
	// reporting nothing is the honest answer — the alternative is reading back
	// far enough to be a full scan of every transcript on the machine.
	LastPrompt string
}

// activityLine is the slice of a transcript record this file reads.
type activityLine struct {
	Timestamp  string `json:"timestamp"`
	Cwd        string `json:"cwd"`
	GitBranch  string `json:"gitBranch"`
	Entrypoint string `json:"entrypoint"`
	Type       string `json:"type"`
	Message    struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// LastActivity reports what a transcript says about itself, reading only its
// tail. False means nothing datable was found near the end, so callers fall back
// rather than being handed a guess.
func LastActivity(path string) (Activity, bool) {
	f, err := os.Open(path)
	if err != nil {
		return Activity{}, false
	}
	defer func() { _ = f.Close() }()
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
		// Read one byte early so the window itself shows whether it opened
		// mid-record: a leading newline means the first record is whole and the
		// trim below keeps it. Without that byte a boundary-aligned window discards
		// a whole record, and at maxTailBytes there is no larger read to recover it.
		read := off
		if read > 0 {
			read--
		}
		buf := make([]byte, size-read)
		if _, rerr := f.ReadAt(buf, read); rerr != nil && !errors.Is(rerr, io.EOF) {
			return Activity{}, false
		}
		if read < off {
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
//
// Context is read off that newest record, so what is displayed describes the same
// moment as the date beside it. Records that carry a timestamp but no context
// exist (queue operations), so a field the newest record leaves empty falls back
// to the nearest one that filled it.
func latestIn(buf []byte) (Activity, bool) {
	var a, nearest Activity
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
		// Scanning backwards, so the first one seen is the nearest to the end.
		if nearest.Cwd == "" {
			nearest.Cwd = rec.Cwd
		}
		if nearest.GitBranch == "" {
			nearest.GitBranch = rec.GitBranch
		}
		if nearest.Entrypoint == "" {
			nearest.Entrypoint = rec.Entrypoint
		}
		// Scanning backwards, so the first typed prompt seen is the last one
		// said. Filtered through the same predicate as the title, so an IDE-state
		// injection or a resumed-session caveat never poses as the last word.
		if nearest.LastPrompt == "" && rec.Type == "user" {
			if text, hasText := PromptCandidate(rec.Message.Content); hasText && IsHumanPrompt(text) {
				nearest.LastPrompt = TidyPrompt(text)
			}
		}
		if rec.Timestamp == "" {
			continue
		}
		t, terr := time.Parse(time.RFC3339, rec.Timestamp)
		if terr != nil {
			continue
		}
		if t = t.UTC(); t.After(a.At) {
			a = Activity{At: t, Cwd: rec.Cwd, GitBranch: rec.GitBranch, Entrypoint: rec.Entrypoint}
		}
	}
	if a.At.IsZero() {
		return Activity{}, false
	}
	if a.Cwd == "" {
		a.Cwd = nearest.Cwd
	}
	if a.GitBranch == "" {
		a.GitBranch = nearest.GitBranch
	}
	if a.Entrypoint == "" {
		a.Entrypoint = nearest.Entrypoint
	}
	// Always the nearest, never the newest record's: the last timestamped record
	// is usually a tool result or a queue operation, which carries no prompt at
	// all, so taking it from `a` would report nothing for almost every session.
	a.LastPrompt = nearest.LastPrompt
	return a, true
}
