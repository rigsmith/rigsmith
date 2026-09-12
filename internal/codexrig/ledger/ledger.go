// Package ledger is the permanent record of every session codexrig has ever
// staged, kept long after the rollout itself has aged out of the sync window.
//
// The synced tree is a rolling window: retention drops rollouts past
// `historyDays`, on every machine. Without a ledger a search for a conversation
// from last year returns nothing, and "no such session" is indistinguishable
// from "that chat never happened" — which is the worse answer, because it is
// wrong and it stops the person looking. A row here turns it into "this existed,
// on this date, in this directory, and its body is in the repo's git history".
//
// One file per device. Two machines appending to one file conflict on every
// sync, and a conflict in the thing that exists to make sessions findable is a
// poor trade for a smaller directory listing.
package ledger

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"
)

// DirName is the ledger directory inside the synced repo.
const DirName = "index"

// Entry is one remembered session.
type Entry struct {
	ID string `json:"id"`
	// Started and End come from the rollout's own records, never from a file's
	// mtime: a restore re-dates whole trees, and a ledger dated by arrival
	// would reorder somebody's history the first time they moved machines.
	Started time.Time `json:"started,omitempty"`
	End     time.Time `json:"end"`
	Bytes   int64     `json:"bytes,omitempty"`

	Cwd        string `json:"cwd,omitempty"`
	Title      string `json:"title,omitempty"`
	Branch     string `json:"branch,omitempty"`
	CLIVersion string `json:"cliVersion,omitempty"`
	// Shard is the rollout's directory, which is where to look in git history.
	Shard string `json:"shard,omitempty"`

	// RecordedBy is the machine whose sync wrote this row — explicitly NOT the
	// machine that ran the session. Codex records no machine identity inside a
	// rollout, so claiming otherwise would be inventing a fact.
	RecordedBy string    `json:"recordedBy,omitempty"`
	Seen       time.Time `json:"seen"`

	// Extra round-trips fields a NEWER codexrig wrote that this one does not
	// understand. Without it, an older binary syncing the same repo would strip
	// them out on every write, and the fleet would converge on whatever the
	// oldest machine knows.
	Extra map[string]json.RawMessage `json:"-"`
}

// Ledger is one device's file, held open for a batch of updates.
type Ledger struct {
	path   string
	device string
	rows   map[string]Entry
	dirty  bool
}

// Open reads (or starts) the ledger file for a device.
func Open(dir, device string) (*Ledger, error) {
	l := &Ledger{
		path:   filepath.Join(dir, DirName, fileName(device)),
		device: device,
		rows:   map[string]Entry{},
	}
	rows, err := readFile(l.path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for _, e := range rows {
		l.rows[e.ID] = e
	}
	return l, nil
}

// Fresh reports whether the ledger already has this session at this size and
// end time — the cheap test that lets a sync skip re-reading a rollout whose
// row cannot have changed.
func (l *Ledger) Fresh(id string, end time.Time, size int64) bool {
	e, ok := l.rows[id]
	return ok && e.Bytes == size && e.End.Equal(end)
}

// Note records or refreshes a session, and reports whether anything changed.
//
// A field that arrives EMPTY never overwrites one that is filled. A sync whose
// tail read failed, or that saw a rollout truncated mid-write, must not blank
// out a title somebody could otherwise have searched for.
func (l *Ledger) Note(e Entry) bool {
	if e.ID == "" {
		return false
	}
	e.Seen = time.Now().UTC().Truncate(time.Second)
	if e.RecordedBy == "" {
		e.RecordedBy = l.device
	}
	prev, had := l.rows[e.ID]
	if had {
		e = carryForward(e, prev)
	}
	if had && sameRow(prev, e) {
		return false
	}
	l.rows[e.ID] = e
	l.dirty = true
	return true
}

// carryForward fills empty fields from the previous row.
func carryForward(e, prev Entry) Entry {
	if e.Cwd == "" {
		e.Cwd = prev.Cwd
	}
	if e.Title == "" {
		e.Title = prev.Title
	}
	if e.Branch == "" {
		e.Branch = prev.Branch
	}
	if e.CLIVersion == "" {
		e.CLIVersion = prev.CLIVersion
	}
	if e.Shard == "" {
		e.Shard = prev.Shard
	}
	if e.Started.IsZero() {
		e.Started = prev.Started
	}
	if e.End.IsZero() {
		e.End = prev.End
	}
	if e.Bytes == 0 {
		e.Bytes = prev.Bytes
	}
	if len(e.Extra) == 0 {
		e.Extra = prev.Extra
	}
	return e
}

// sameRow compares everything a reader would notice, ignoring Seen — which
// moves on every sync and would otherwise rewrite the whole file each run.
func sameRow(a, b Entry) bool {
	a.Seen, b.Seen = time.Time{}, time.Time{}
	return reflect.DeepEqual(a, b)
}

// Count is how many sessions this device's ledger remembers.
func (l *Ledger) Count() int { return len(l.rows) }

// Save writes the file, sorted by id so a diff shows what changed rather than
// what moved. Whole-file via a temp and a rename: for an aged-out session this
// row is the only record left, and a half-written ledger would lose it.
func (l *Ledger) Save() error {
	if !l.dirty {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(l.path), 0o755); err != nil {
		return err
	}
	ids := make([]string, 0, len(l.rows))
	for id := range l.rows {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var b strings.Builder
	for _, id := range ids {
		line, err := marshal(l.rows[id])
		if err != nil {
			return err
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	tmp := l.path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, l.path); err != nil {
		return err
	}
	l.dirty = false
	return nil
}

// LoadAll reads every device's ledger into one index, newest row winning where
// two machines remember the same session.
func LoadAll(dir string) map[string]Entry {
	out := map[string]Entry{}
	entries, err := os.ReadDir(filepath.Join(dir, DirName))
	if err != nil {
		return out
	}
	for _, f := range entries {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".jsonl") || strings.HasPrefix(f.Name(), ".") {
			continue
		}
		rows, err := readFile(filepath.Join(dir, DirName, f.Name()))
		if err != nil {
			continue
		}
		for _, e := range rows {
			cur, have := out[e.ID]
			if !have || newer(e, cur) {
				out[e.ID] = e
			}
		}
	}
	return out
}

// newer decides between two machines' memory of one session: the later END
// wins, because that is the machine that saw more of the conversation. Seen is
// only a tie-break — it says when a sync ran, not how much it saw.
func newer(a, b Entry) bool {
	if !a.End.Equal(b.End) {
		return a.End.After(b.End)
	}
	if a.Bytes != b.Bytes {
		return a.Bytes > b.Bytes
	}
	return a.Seen.After(b.Seen)
}

// --- serialisation, preserving fields this binary does not know -----------

var knownFields = func() map[string]bool {
	m := map[string]bool{}
	t := reflect.TypeOf(Entry{})
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("json")
		name, _, _ := strings.Cut(tag, ",")
		if name != "" && name != "-" {
			m[name] = true
		}
	}
	return m
}()

func marshal(e Entry) ([]byte, error) {
	b, err := json.Marshal(e)
	if err != nil {
		return nil, err
	}
	if len(e.Extra) == 0 {
		return b, nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	for k, v := range e.Extra {
		if !knownFields[k] {
			m[k] = v
		}
	}
	return json.Marshal(m)
}

func unmarshal(line []byte) (Entry, error) {
	var e Entry
	if err := json.Unmarshal(line, &e); err != nil {
		return e, err
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(line, &m); err != nil {
		return e, err
	}
	for k, v := range m {
		if !knownFields[k] {
			if e.Extra == nil {
				e.Extra = map[string]json.RawMessage{}
			}
			e.Extra[k] = v
		}
	}
	return e, nil
}

func readFile(path string) ([]Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		e, err := unmarshal([]byte(line))
		if err != nil || e.ID == "" {
			continue // a truncated line is not a reason to lose the rest
		}
		out = append(out, e)
	}
	return out, sc.Err()
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// fileName maps a device name to its ledger file. Sanitised, because a device
// name comes from a hostname and a hostname is not a filename.
func fileName(device string) string {
	n := strings.Trim(unsafeName.ReplaceAllString(device, "-"), ".-")
	if n == "" {
		n = "unknown"
	}
	return n + ".jsonl"
}
