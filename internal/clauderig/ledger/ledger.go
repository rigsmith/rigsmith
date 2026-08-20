// Package ledger models index/<device>.jsonl — a permanent record of every
// Claude Code session sync has ever staged, kept long after the transcript
// itself ages out.
//
// It exists because the synced repo is a rolling window, not an archive: sync
// drops project transcripts older than the retention setting, and `search` then
// returns nothing for them — indistinguishable, to the person asking, from the
// chat never having existed. The bodies are still recoverable from the sync
// repo's git history; what is lost is knowing there is anything to recover.
//
// A row is a few hundred bytes and is never deleted, so the ledger stays
// searchable by title, project, and date for the entire life of the repo.
package ledger

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// DirName is the ledger directory at the sync repo root. One file per device,
// because two machines appending to a single shared file is a merge conflict on
// every sync; separate paths merge cleanly and are trivially unioned on read.
const DirName = "index"

// Entry is one session's permanent record.
type Entry struct {
	ID   string `json:"id"`
	Slug string `json:"slug,omitempty"`
	// Cwd is the project directory as the transcript recorded it — an absolute
	// path on whichever machine ran the session, so readers resolve it through
	// their own machine mapping rather than assuming it exists here.
	Cwd string `json:"cwd,omitempty"`
	// Title is the session's first prompt, the same fallback title `search`
	// shows for a session with no Desktop sidecar.
	Title string `json:"title,omitempty"`
	// End is the transcript's last-modified time — the session's date.
	End time.Time `json:"end"`
	// Bytes is the transcript size; with End it forms the change fingerprint
	// that decides whether a row needs rewriting.
	Bytes int64 `json:"bytes,omitempty"`
	// RecordedBy is the machine whose sync wrote this row. Deliberately NOT
	// "the machine that ran the session": staging holds every machine's
	// transcripts, so any device's sync may be the one that first sees a row.
	// Naming it for what it actually is keeps it from becoming a --device
	// filter that quietly lies.
	RecordedBy string `json:"recordedBy,omitempty"`
	// Seen is when this row was last written.
	Seen time.Time `json:"seen"`
}

// Ledger is one device's file, loaded for update.
type Ledger struct {
	dir    string
	device string
	rows   map[string]Entry
	dirty  bool
}

// Open loads (or starts) the ledger file for device under dir.
func Open(dir, device string) (*Ledger, error) {
	l := &Ledger{dir: dir, device: device, rows: map[string]Entry{}}
	rows, err := readFile(l.path())
	if err != nil {
		return nil, err
	}
	for _, e := range rows {
		l.rows[e.ID] = e
	}
	return l, nil
}

func (l *Ledger) path() string {
	return filepath.Join(l.dir, DirName, fileNameFor(l.device))
}

// fileNameFor turns a machine name into one safe filename component. The name
// comes from config.json's machines map or the hostname, so it is not guaranteed
// to be path-safe — and a name containing a separator would put the file outside
// index/, where LoadAll never looks, leaving that machine's ledger silently
// unread rather than failing. The row keeps the original name in RecordedBy.
func fileNameFor(device string) string {
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '-', r == '_', r == '.':
			return r
		default:
			return '-'
		}
	}, device)
	safe = strings.Trim(safe, ".-")
	if safe == "" {
		safe = "device"
	}
	return safe + ".jsonl"
}

// Note records a session, or updates the row when the transcript has changed
// since it was last seen. Rows are never removed: a session that has aged out
// of the synced tree is exactly the one worth remembering. Returns true when
// something was written.
func (l *Ledger) Note(e Entry) bool {
	if e.ID == "" {
		return false
	}
	if prev, ok := l.rows[e.ID]; ok {
		if prev.Bytes == e.Bytes && prev.End.Equal(e.End) {
			return false
		}
		// The same session id can appear under two slugs — a transcript copied into
		// a worktree's project dir, or a slug rewritten by restore. Keep the newest
		// and ignore the older twin, or the two rewrite each other on every sync
		// forever, which costs a commit per sync for a file nothing changed in.
		// Measured on a real tree: 5 such ids, 10 pointless row writes per pass.
		if prev.End.After(e.End) {
			return false
		}
	}
	e.RecordedBy = l.device
	l.rows[e.ID] = e
	l.dirty = true
	return true
}

// Fresh reports whether the ledger already holds this exact transcript state,
// so callers can skip the file read that computes a title.
func (l *Ledger) Fresh(id string, end time.Time, size int64) bool {
	prev, ok := l.rows[id]
	return ok && prev.Bytes == size && prev.End.Equal(end)
}

// Save rewrites the device's file, sorted by id. Rewriting rather than
// appending is deliberate: git stores whole blobs either way, so appending buys
// nothing, while a sorted file has a readable diff and is byte-identical when
// nothing changed — which is what keeps an idle sync from committing at all.
func (l *Ledger) Save() error {
	if !l.dirty {
		return nil
	}
	if err := os.MkdirAll(filepath.Join(l.dir, DirName), 0o755); err != nil {
		return err
	}
	ids := make([]string, 0, len(l.rows))
	for id := range l.rows {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var b strings.Builder
	for _, id := range ids {
		row, err := json.Marshal(l.rows[id])
		if err != nil {
			return err
		}
		b.Write(row)
		b.WriteByte('\n')
	}
	return os.WriteFile(l.path(), []byte(b.String()), 0o644)
}

// Count is how many sessions this device's ledger remembers.
func (l *Ledger) Count() int { return len(l.rows) }

// LoadAll unions every device's ledger under dir, newest row per session id
// winning. Absent or unreadable files contribute nothing — the ledger is a
// convenience for search, never a reason for it to fail.
func LoadAll(dir string) map[string]Entry {
	out := map[string]Entry{}
	entries, err := os.ReadDir(filepath.Join(dir, DirName))
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		rows, err := readFile(filepath.Join(dir, DirName, e.Name()))
		if err != nil {
			continue
		}
		for _, r := range rows {
			if prev, ok := out[r.ID]; ok && !newerRow(r, prev) {
				continue
			}
			out[r.ID] = r
		}
	}
	return out
}

// newerRow reports whether a is the better row for a session two devices both
// recorded. End first, Seen only as the tiebreak: End describes the SESSION, Seen
// merely when a machine last looked — so a device syncing an older copy of a
// transcript today must not walk the session's date backwards.
func newerRow(a, b Entry) bool {
	if !a.End.Equal(b.End) {
		return a.End.After(b.End)
	}
	return a.Seen.After(b.Seen)
}

// readFile parses one ledger file, skipping malformed lines rather than failing
// the whole read — a truncated write must not cost every other row.
func readFile(path string) ([]Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var out []Entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e Entry
		if json.Unmarshal([]byte(line), &e) != nil || e.ID == "" {
			continue
		}
		out = append(out, e)
	}
	return out, sc.Err()
}
