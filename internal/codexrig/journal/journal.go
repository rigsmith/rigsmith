// Package journal is the durable record of what every sync did, kept inside the
// synced repo so any machine can read any machine's history.
//
// It exists because of a failure mode clauderig hit first and codexrig would hit
// identically: the sync runs from a hook, so when it refuses — a credential the
// tripwire caught, a push rejected — the only place that says so is the hook's
// stderr, which nobody reads. Days can pass with nothing backed up and no
// visible sign. A record inside the repo makes "when did this last work" a
// question with an answer.
//
// One file per machine, because two machines appending to one file conflict on
// every sync. The ordering rule for callers is APPEND BEFORE COMMITTING: the
// record lives in the tree being committed, so a record written afterwards does
// not ride the sync it describes.
package journal

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// DirName is the journal directory inside the synced repo.
const DirName = "journal"

// MaxRecords is how many records one machine's file keeps before the oldest are
// dropped. The journal is a log, not an archive; git history holds the rest.
const MaxRecords = 1000

// MaxRedactedFiles bounds how many redacted files one record names. A run that
// scrubbed two thousand rollouts should say so, not list them.
const MaxRedactedFiles = 25

// Op is what a run was doing.
type Op string

const (
	OpSync    Op = "sync"
	OpPull    Op = "pull"
	OpRestore Op = "restore"
)

// Outcome is how it went. `refused` is deliberately distinct from `failed`: a
// tripwire refusal is the tool working correctly and needs a different message
// from a push that broke.
type Outcome string

const (
	OutcomeOK      Outcome = "ok"
	OutcomeFailed  Outcome = "failed"
	OutcomeRefused Outcome = "refused"
)

// RedactedFile names one file the redactor changed, and what kind of thing it
// took out — kinds and field paths, never values. This travels inside the synced
// repo, so it has to be a map of where secrets were, not a second copy of them.
type RedactedFile struct {
	Path  string   `json:"path"`
	Kinds []string `json:"kinds,omitempty"`
	Paths []string `json:"paths,omitempty"`
	Count int      `json:"count,omitempty"`
}

// Leak is one tripwire finding: a value or a whole file that looked like a
// credential and was not redacted.
type Leak struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
	File bool   `json:"file,omitempty"`
}

// Record is one run.
type Record struct {
	At      time.Time `json:"at"`
	Machine string    `json:"machine"`
	Op      Op        `json:"op"`
	Outcome Outcome   `json:"outcome"`
	Error   string    `json:"error,omitempty"`

	Files      int `json:"files,omitempty"`
	Unchanged  int `json:"unchanged,omitempty"`
	Redactions int `json:"redactions,omitempty"`
	Skipped    int `json:"skipped,omitempty"`
	Deferred   int `json:"deferred,omitempty"`
	Disallowed int `json:"disallowed,omitempty"`
	// AgedOut is an event — files pruned by this run. TooOld is a standing
	// condition, recounted every run. Keeping them apart is what stops a
	// summary reporting the same thousand old rollouts as news every time.
	AgedOut  int `json:"agedOut,omitempty"`
	TooOld   int `json:"tooOld,omitempty"`
	Oversize int `json:"oversize,omitempty"`

	Leaks         []Leak         `json:"leaks,omitempty"`
	RedactedFiles []RedactedFile `json:"redactedFiles,omitempty"`
}

// OK reports whether a record describes a run that worked.
func (r Record) OK() bool { return r.Outcome == OutcomeOK }

// Summary is the one-line form a status screen prints.
func (r Record) Summary() string {
	switch r.Outcome {
	case OutcomeRefused:
		n := len(r.Leaks)
		if n == 0 {
			return "Sync refused"
		}
		return "Sync refused — " + plural(n, "value", "values") + " look like credentials"
	case OutcomeFailed:
		s := "Last " + string(r.Op) + " failed"
		if r.Error != "" {
			s += ": " + r.Error
		}
		return s
	}
	switch r.Op {
	case OpRestore:
		return "Restored " + plural(r.Files, "file", "files")
	case OpPull:
		return "Pulled"
	default:
		if r.Files == 0 {
			return "Nothing changed"
		}
		return "Synced " + plural(r.Files, "file", "files")
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

// Failed and Succeeded build the records for a run with nothing else to report.
func Failed(machine string, op Op, err error) Record {
	r := Record{At: time.Now().UTC(), Machine: machine, Op: op, Outcome: OutcomeFailed}
	if err != nil {
		r.Error = err.Error()
	}
	return r
}

func Succeeded(machine string, op Op) Record {
	return Record{At: time.Now().UTC(), Machine: machine, Op: op, Outcome: OutcomeOK}
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// fileName maps a machine name to its journal file. Sanitised because a machine
// name comes from a hostname, and a hostname is not a filename.
func fileName(machine string) string {
	n := strings.Trim(unsafeName.ReplaceAllString(machine, "-"), ".-")
	if n == "" {
		n = "unknown"
	}
	return n + ".jsonl"
}

// Append adds one record to this machine's file. A single append write, which is
// atomic at these sizes, and a symlink at the destination is refused before
// anything is written.
func Append(dir string, rec Record) error {
	d := filepath.Join(dir, DirName)
	if err := os.MkdirAll(d, 0o755); err != nil {
		return err
	}
	p := filepath.Join(d, fileName(rec.Machine))
	if st, err := os.Lstat(p); err == nil && st.Mode()&os.ModeSymlink != 0 {
		return &os.PathError{Op: "append", Path: p, Err: os.ErrInvalid}
	}
	if len(rec.RedactedFiles) > MaxRedactedFiles {
		rec.RedactedFiles = rec.RedactedFiles[:MaxRedactedFiles]
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return compact(p)
}

// compact trims a machine's file to the newest MaxRecords.
func compact(path string) error {
	recs, err := readFile(path)
	if err != nil || len(recs) <= MaxRecords {
		return nil //nolint:nilerr // a journal that cannot be trimmed is not a reason to fail a sync
	}
	keep := recs[len(recs)-MaxRecords:]
	var buf strings.Builder
	for _, r := range keep {
		b, err := json.Marshal(r)
		if err != nil {
			return err
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(buf.String()), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Read returns records from every machine, newest first, at most limit (0 = all).
func Read(dir string, limit int) ([]Record, error) {
	d := filepath.Join(dir, DirName)
	entries, err := os.ReadDir(d)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var all []Record
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		recs, err := readFile(filepath.Join(d, e.Name()))
		if err != nil {
			continue
		}
		// Within one append-only file, line order is the only tie-break for
		// records that share a timestamp — so reverse each file before merging
		// rather than sorting the union and losing it.
		for i := len(recs) - 1; i >= 0; i-- {
			all = append(all, recs[i])
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].At.After(all[j].At) })
	if limit > 0 && len(all) > limit {
		all = all[:limit]
	}
	return all, nil
}

func readFile(path string) ([]Record, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Record
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var r Record
		if json.Unmarshal([]byte(line), &r) != nil {
			continue // a truncated line is not a reason to lose the rest
		}
		out = append(out, r)
	}
	return out, sc.Err()
}

// LastSuccessful returns the newest successful record for one machine and op —
// what the hook debounce consults.
//
// It reads the WHOLE feed rather than the newest few: on a three-machine fleet
// the newest forty records can hold none of this machine's, and a debounce that
// cannot find its last sync stops debouncing.
func LastSuccessful(dir, machine string, op Op) (Record, bool) {
	recs, err := Read(dir, 0)
	if err != nil {
		return Record{}, false
	}
	for _, r := range recs {
		if r.Machine == machine && r.Op == op && r.OK() {
			return r, true
		}
	}
	return Record{}, false
}
