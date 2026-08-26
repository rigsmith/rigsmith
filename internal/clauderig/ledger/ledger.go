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
	"reflect"
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
	// Account is the accountUuid of the Claude Code login this session belongs
	// to, empty when unknown. CLI transcripts carry no account field of their
	// own, so this is the only place a session's account is ever recorded — and
	// it cannot be reconstructed later, which is why it is captured here rather
	// than derived at query time.
	Account string `json:"account,omitempty"`
	// AccountSource says HOW Account was determined, because the two ways differ
	// sharply in confidence and a filter that hid that would be the kind of
	// quiet lie RecordedBy is named to avoid. See AccountFrom*.
	AccountSource string `json:"accountSource,omitempty"`
	// Extra holds fields this binary does not know about, so a row written by a
	// NEWER clauderig survives a round trip through this one instead of being
	// silently dropped on the next rewrite.
	//
	// It does not rescue rows from an OLDER binary — that one has no such
	// catch-all, so it drops Account/AccountSource/AccountSince when it rewrites
	// a row whose transcript changed. Attribution re-derives on the next sync
	// from a Desktop sidecar or this machine's own login, so the loss is not
	// permanent; what is lost is the FIRST-attribution stamp, which is why
	// mixed-version fleets should update together.
	Extra map[string]json.RawMessage `json:"-"`
	// AccountSince is when the CURRENT attribution was established, and it does
	// not move when the transcript changes.
	//
	// Seen cannot serve here, which is subtle enough to be worth stating: Seen
	// is the row's last-write time, so a machine merely re-syncing a changed
	// transcript pushes its Seen past another machine's — and a tie-break on
	// Seen would then hand the session to whichever device wrote most recently,
	// which is the opposite of the first-attribution rule it is meant to
	// enforce. Only a stamp tied to the attribution itself is stable.
	AccountSince time.Time `json:"accountSince,omitempty"`
}

// How an Entry.Account was determined, worst to best. A higher rank may
// overwrite a lower one; equal ranks never overwrite, so the first sync to
// attribute a session wins and later syncs cannot relabel it.
const (
	// AccountFromSync is an INFERENCE: the account the syncing machine was
	// logged in as when it first recorded the row. Right for the ordinary case
	// — you sync the machine you work on — and wrong if you switched accounts
	// between running the session and syncing it, or if this machine restored
	// another machine's transcripts and staged them as its own. Sticky for
	// exactly that reason: the earliest sighting is the closest to the truth.
	AccountFromSync = "sync"
	// AccountFromDesktop is GROUND TRUTH, taken from the path Claude Desktop
	// files its session sidecar under —
	// claude-code-sessions/<accountUuid>/<organizationUuid>/ — which is the
	// account itself, not a guess about it. Only sessions opened through
	// Desktop have one (measured: 3% of a real staged tree), so it upgrades
	// what it covers and leaves the rest to the inference above.
	AccountFromDesktop = "desktop"
)

// AccountRank orders the sources; an unattributed row ranks lowest. Exported so
// the recorder can tell, before reading a transcript, whether the attribution it
// is about to offer would actually improve on the stored one.
func AccountRank(source string) int {
	switch source {
	case AccountFromDesktop:
		return 2
	case AccountFromSync:
		return 1
	}
	return 0
}

// bestAccount picks the attribution to surface when two devices hold a row for
// the same session.
//
// Rank first. On EQUAL rank the EARLIER sighting wins, which is what makes the
// stickiness promise hold across devices as well as within a file: mergeAccount
// keeps its first argument on a tie, and the caller's first argument is the
// NEWER row, so using it here would let a second machine recording an
// already-staged transcript under its own live login relabel the session on a
// routine sync — the exact relabelling Note() refuses to do locally.
func bestAccount(a, b Entry) (account, source string, since time.Time) {
	ra, rb := AccountRank(a.AccountSource), AccountRank(b.AccountSource)
	// effectiveSince on the rank path too. A legacy row that wins on RANK still
	// carries a zero stamp otherwise, and the merged row then falls back to its
	// own newer Seen on the next merge — letting a third equal-rank row displace
	// the attribution that was genuinely first.
	if rb > ra {
		return b.Account, b.AccountSource, effectiveSince(b)
	}
	if ra > rb {
		return a.Account, a.AccountSource, effectiveSince(a)
	}
	// Equal rank: the earlier ATTRIBUTION wins. Tying on Seen instead would let
	// a row whose transcript was merely re-synced displace the first
	// attribution, and would not even be stable — a merged row carries the
	// newer transcript's Seen alongside an older row's account, so a third
	// device with an intermediate Seen could take the session from both.
	sa, sb := effectiveSince(a), effectiveSince(b)
	// Return the EFFECTIVE stamp. Handing back a legacy row's raw zero would
	// attach it to the merged winner, whose own Seen is newer — so the next
	// merge would fall back to THAT, and a third equal-rank row could displace
	// an attribution this comparison just decided was the earliest.
	if sb.Before(sa) {
		return b.Account, b.AccountSource, sb
	}
	if sa.Before(sb) {
		return a.Account, a.AccountSource, sa
	}
	// Identical stamps — including the case where two machines' clocks disagree
	// enough to produce them. Wall-clock order cannot be trusted to say which
	// attribution came first, so the tie is broken on the account uuid instead:
	// it is arbitrary, but it is the SAME arbitrary answer on every machine,
	// which is what stops the union from flip-flopping between devices. Skew
	// can still crown the wrong "earliest"; it can no longer make them disagree.
	if b.Account < a.Account {
		return b.Account, b.AccountSource, sb
	}
	return a.Account, a.AccountSource, sa
}

// effectiveSince is the attribution's timestamp, falling back to the row's write
// time for rows written before AccountSince existed.
//
// Without the fallback a legacy row — real attribution, zero stamp — loses to
// anything written afterwards, because a zero time precedes everything. That
// would quietly hand every historical session to whichever machine synced next.
func effectiveSince(e Entry) time.Time {
	if !e.AccountSince.IsZero() {
		return e.AccountSince
	}
	return e.Seen
}

// mergeAccount picks the attribution to keep. Ties go to prev — attribution is
// sticky, so re-syncing a session under a different login cannot rewrite whose
// it was.
func mergeAccount(prev, next Entry) (account, source string, since time.Time) {
	if next.Account != "" && AccountRank(next.AccountSource) > AccountRank(prev.AccountSource) {
		// A genuinely new attribution: stamp it now, since this is the moment it
		// was established.
		return next.Account, next.AccountSource, next.AccountSince
	}
	return prev.Account, prev.AccountSource, prev.AccountSince
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
	// A first attribution is established at this write; every later write
	// carries the stamp forward through mergeAccount rather than restamping it.
	if e.Account != "" && e.AccountSince.IsZero() {
		e.AccountSince = e.Seen
	}
	if prev, ok := l.rows[e.ID]; ok {
		// Migrate a legacy row, so the fallback in effectiveSince is only ever
		// needed until each row is next written.
		if prev.Account != "" && prev.AccountSince.IsZero() {
			prev.AccountSince = prev.Seen
			l.rows[e.ID] = prev
		}
		// Carry unknown fields across. The incoming row is built by this binary
		// and cannot know about them, so replacing the stored row wholesale is
		// exactly where a newer clauderig's data would be lost.
		if e.Extra == nil {
			e.Extra = prev.Extra
		}
		e.Account, e.AccountSource, e.AccountSince = mergeAccount(prev, e)
		// An account upgrade is a real change even when the transcript is byte
		// identical — a session first attributed by inference and later covered
		// by its Desktop sidecar must be allowed to take the better answer.
		upgraded := e.Account != prev.Account || e.AccountSource != prev.AccountSource
		if prev.Bytes == e.Bytes && prev.End.Equal(e.End) && !upgraded {
			return false
		}
		// The same session id can appear under two slugs — a transcript copied into
		// a worktree's project dir, or a slug rewritten by restore. Keep the newest
		// and ignore the older twin, or the two rewrite each other on every sync
		// forever, which costs a commit per sync for a file nothing changed in.
		// Measured on a real tree: 5 such ids, 10 pointless row writes per pass.
		if prev.End.After(e.End) {
			if !upgraded {
				return false
			}
			// Take ONLY the better attribution; the older twin must not drag its
			// stale transcript state over the newer row.
			prev.Account, prev.AccountSource, prev.AccountSince = e.Account, e.AccountSource, e.AccountSince
			prev.RecordedBy = l.device
			// The row IS being rewritten, so Seen has to move with it — a stale
			// stamp would misreport when this attribution was last confirmed,
			// and Seen is a tiebreak in the cross-device union.
			prev.Seen = e.Seen
			l.rows[e.ID] = prev
			l.dirty = true
			return true
		}
	}
	e.RecordedBy = l.device
	l.rows[e.ID] = e
	l.dirty = true
	return true
}

// Revoke clears a session's recorded attribution, reporting whether anything
// changed.
//
// Ranked merging deliberately has no downgrade path — that is what makes
// attribution sticky — so a session that BECOMES contested needs an explicit
// way out. Without it, the ground truth recorded before the conflict keeps
// filtering the session under an account that is now disputed, and the higher
// rank guarantees no later answer can correct it.
func (l *Ledger) Revoke(id string) bool {
	e, ok := l.rows[id]
	if !ok || (e.Account == "" && e.AccountSource == "") {
		return false
	}
	e.Account, e.AccountSource, e.AccountSince = "", "", time.Time{}
	l.rows[id] = e
	l.dirty = true
	return true
}

// Attribution reports the account currently recorded for a session, so a caller
// can decide whether a better source is worth a rewrite. Empty when the session
// is unknown or unattributed.
func (l *Ledger) Attribution(id string) (account, source string) {
	e := l.rows[id]
	return e.Account, e.AccountSource
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
		row, err := marshalEntry(l.rows[id])
		if err != nil {
			return err
		}
		b.Write(row)
		b.WriteByte('\n')
	}
	// Written to a temporary file in the same directory and renamed over the
	// destination. The ledger is the ONLY copy of an aged-out session — no
	// transcript can reconstruct it — so a truncating write interrupted midway
	// would permanently lose rows that exist nowhere else. Rename is atomic
	// within a directory, so a reader sees the old file or the new one.
	dst := l.path()
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".ledger-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename succeeds
	if _, err := tmp.WriteString(b.String()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpName, dst)
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
		// Skip the dot-prefixed temporaries a Save in flight may have left.
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		rows, err := readFile(filepath.Join(dir, DirName, e.Name()))
		if err != nil {
			continue
		}
		for _, r := range rows {
			prev, ok := out[r.ID]
			if !ok {
				out[r.ID] = r
				continue
			}
			winner, loser := prev, r
			if newerRow(r, prev) {
				winner, loser = r, prev
			}
			// Attribution follows RANK across devices, not recency. Note()
			// enforces that within one device's file, but the union is where two
			// files meet: without this, a machine that later re-saw the same
			// transcript with no sidecar would replace another machine's Desktop
			// ground truth with its own inference purely by having synced last.
			winner.Account, winner.AccountSource, winner.AccountSince = bestAccount(winner, loser)
			out[r.ID] = winner
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
// knownEntryFields is the JSON key set this binary understands, derived from the
// struct tags so adding a field cannot forget to update it.
var knownEntryFields = func() map[string]bool {
	out := map[string]bool{}
	t := reflect.TypeOf(Entry{})
	for i := 0; i < t.NumField(); i++ {
		name := strings.Split(t.Field(i).Tag.Get("json"), ",")[0]
		if name != "" && name != "-" {
			out[name] = true
		}
	}
	return out
}()

// marshalEntry writes a row, folding back any fields this binary did not
// recognise when it read them. Rewriting a row is otherwise a lossy operation
// for anything a newer clauderig added.
func marshalEntry(e Entry) ([]byte, error) {
	row, err := json.Marshal(e)
	if err != nil {
		return nil, err
	}
	var merged map[string]json.RawMessage
	if err := json.Unmarshal(row, &merged); err != nil {
		return row, nil //nolint:nilerr // a row that will not round-trip is still better written than dropped
	}
	// `omitempty` does not suppress a zero time.Time — it is a struct, never
	// "empty" to encoding/json — so an unattributed row would carry a literal
	// year-1 timestamp into every ledger file. Drop it here instead.
	if e.AccountSince.IsZero() {
		delete(merged, "accountSince")
	}
	for k, v := range e.Extra {
		if !knownEntryFields[k] {
			merged[k] = v
		}
	}
	return json.Marshal(merged)
}

// unknownFields returns the keys of line that this binary does not model.
func unknownFields(line []byte) map[string]json.RawMessage {
	var raw map[string]json.RawMessage
	if json.Unmarshal(line, &raw) != nil {
		return nil
	}
	for k := range raw {
		if knownEntryFields[k] {
			delete(raw, k)
		}
	}
	if len(raw) == 0 {
		return nil
	}
	return raw
}

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
		// A decode error can still have populated earlier fields — ID among
		// them — before failing on a later one. Appending that partial record
		// would surface it as a searchable session with missing metadata, and
		// the next save would rewrite the row from those zero values.
		if json.Unmarshal([]byte(line), &e) != nil {
			continue
		}
		e.Extra = unknownFields([]byte(line))
		if e.ID == "" {
			continue
		}
		out = append(out, e)
	}
	return out, sc.Err()
}
