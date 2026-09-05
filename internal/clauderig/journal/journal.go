// Package journal is clauderig's durable record of what each sync, pull, and
// restore actually did.
//
// It exists because outcomes used to be ephemeral. The Stop hook's sync refused
// to run for days with "Secret tripwire: 12 value(s) look like credentials",
// and the only place that message ever appeared was hook stderr, which nobody
// reads. Meanwhile `status` and the device registry both reported health from
// the last *push*, so a machine could sit 65 commits behind while claiming it
// had synced minutes ago. A record on disk is what makes a failure survive the
// process that produced it.
//
// # Layout
//
// One append-only JSONL file per machine, under <staging>/journal/:
//
//	journal/Johns-MacBook-Pro16.jsonl
//	journal/Johns-MacBook-Air13.jsonl
//
// Per-machine rather than one shared file, so the journal syncs across machines
// without ever conflicting: each machine only writes its own file, and git
// merges disjoint files without help. A shared file would conflict on nearly
// every sync and land squarely in the merge mess this whole effort is about.
//
// # Ordering
//
// Records live inside the staging repo, so they are committed and pushed by the
// same sync they describe. That imposes an ordering rule on callers: append
// *before* committing. A record written afterwards leaves the tree dirty until
// the next run, which would make `status` report uncommitted changes forever.
// The exception is a git-phase failure — a record saying "push failed" cannot
// ride the push that failed, so it waits for the next sync. A dirty tree after
// a failure is a true signal, so that case needs no special handling.
package journal

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// DirName is the journal's directory inside the staging repo.
const DirName = "journal"

// MaxRecords bounds one machine's file. Records are a few hundred bytes, so
// this is well under a megabyte — the journal must never become the thing that
// wedges a sync, which oversized files have already done once.
const MaxRecords = 1000

// MaxRedactedFiles bounds one record's file list. A first sync over a tree full
// of MCP configs can redact hundreds; the journal is a feed, not an inventory.
const MaxRedactedFiles = 25

// Op is the operation a record describes.
type Op string

const (
	OpSync    Op = "sync"
	OpPull    Op = "pull"
	OpRestore Op = "restore"
	OpMerge   Op = "merge"
)

// Outcome is how the operation ended.
type Outcome string

const (
	// OutcomeOK — the operation completed.
	OutcomeOK Outcome = "ok"
	// OutcomeFailed — it errored.
	OutcomeFailed Outcome = "failed"
	// OutcomeRefused — clauderig declined to proceed on purpose, the tripwire
	// being the case that matters. Distinct from Failed because the safety
	// property worked; the UI should not render it as a malfunction.
	OutcomeRefused Outcome = "refused"
)

// Leak is one tripwire finding: a value that looked like a credential.
// RedactedFile is one file the redactor cleaned on the way into staging.
type RedactedFile struct {
	Path  string   `json:"path"`
	Kinds []string `json:"kinds,omitempty"`
	// Paths is the same map for structured files, which are redacted by field:
	// dotted JSON paths, never values.
	Paths []string `json:"paths,omitempty"`
	Count int      `json:"count,omitempty"`
}

type Leak struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
}

// Record is one line of the journal.
type Record struct {
	At      time.Time `json:"at"`
	Machine string    `json:"machine"`
	Op      Op        `json:"op"`
	Outcome Outcome   `json:"outcome"`
	// Error is the failure text, already trimmed to one line.
	Error string `json:"error,omitempty"`

	Files int `json:"files,omitempty"`
	// Unchanged is what the sync looked at and left alone. Without it a quiet
	// sync and a broken one both read as "0 files", and there is no way to tell
	// "nothing had changed" from "nothing got through".
	Unchanged  int `json:"unchanged,omitempty"`
	Redactions int `json:"redactions,omitempty"`
	// AgedOut is files REMOVED from staging this run because they passed the
	// retention window — an event, and normally zero.
	AgedOut int `json:"agedOut,omitempty"`
	// TooOld is files in the live tree the sync declined to stage because they
	// are already past the window. It is a standing condition, not an event:
	// clauderig never deletes from ~/.claude, so the same files are re-counted
	// on every run and the number holds steady until they are removed by hand.
	// Kept out of Summary for that reason — folded into AgedOut it made a real
	// prune indistinguishable from the constant.
	TooOld   int `json:"tooOld,omitempty"`
	Oversize int `json:"oversize,omitempty"`
	Skipped  int `json:"skipped,omitempty"`
	Projects int `json:"projects,omitempty"`

	Leaks []Leak `json:"leaks,omitempty"`

	// RedactedFiles names the files behind Redactions. The count alone said a
	// secret was caught but not where, which is the only part anyone can act on.
	//
	// Kinds, never values — this is written into a file that syncs. It is a map
	// of where credentials turned up, which is worth having and is not itself a
	// credential.
	RedactedFiles []RedactedFile `json:"redactedFiles,omitempty"`
}

// OK reports whether the record describes a clean run.
func (r Record) OK() bool { return r.Outcome == OutcomeOK }

// Append writes rec to its machine's file under dir/journal, creating both as
// needed, and compacts the file when it exceeds MaxRecords.
//
// The write is a single O_APPEND call, which POSIX keeps atomic at these sizes
// — so a Stop-hook sync and a hand-run sync cannot interleave halves of a line.
// Compaction is a read-rewrite and is not atomic against a concurrent append,
// but it runs once per MaxRecords writes and the loss is bounded to a record.
func Append(dir string, rec Record) error {
	if rec.At.IsZero() {
		rec.At = time.Now()
	}
	rec.At = rec.At.UTC()
	rec.Error = oneLine(rec.Error)

	jdir := filepath.Join(dir, DirName)
	if err := os.MkdirAll(jdir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(jdir, fileName(rec.Machine))

	line, err := json.Marshal(rec)
	if err != nil {
		return err
	}

	// The journal lives in a repo that is cloned from elsewhere, so its contents
	// arrive from another machine. A symlink checked out here would have this
	// append follow it and write outside the staging tree entirely.
	if info, lerr := os.Lstat(path); lerr == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("journal %s is a symlink — refusing to write through it", path)
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return compact(path)
}

// Read returns records from every machine's file, newest first, capped at limit
// (limit <= 0 returns everything).
//
// Malformed lines are skipped rather than failing the read: a torn write must
// cost one row of the activity feed, not the whole feed. Same for an unreadable
// file — one machine's bad journal shouldn't hide the others'.
func Read(dir string, limit int) ([]Record, error) {
	jdir := filepath.Join(dir, DirName)
	entries, err := os.ReadDir(jdir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // nothing has been journalled yet
		}
		return nil, err
	}

	var out []Record
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		recs, err := readFile(filepath.Join(jdir, e.Name()))
		if err != nil {
			continue
		}
		out = append(out, recs...)
	}

	// Newest first; ties broken by machine so the order is stable across reads.
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].At.Equal(out[j].At) {
			return out[i].At.After(out[j].At)
		}
		return out[i].Machine < out[j].Machine
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func readFile(path string) ([]Record, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []Record
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var rec Record
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue // torn or hand-edited line — drop the row, keep the feed
		}
		out = append(out, rec)
	}
	return out, sc.Err()
}

// compact trims path to the newest MaxRecords lines. It rewrites via a temp
// file and rename so a crash mid-compaction leaves the original intact.
func compact(path string) error {
	lines, err := readLines(path)
	if err != nil {
		return err
	}
	if len(lines) <= MaxRecords {
		return nil
	}

	// Rewriting the file is the one operation here that can lose a record: an
	// append that lands between the read above and the rename below is not in
	// what gets written, and the rename drops it. Appends themselves are
	// O_APPEND single lines and do not interleave, so it is enough that only one
	// compaction runs at a time — and if another already holds it, skipping is
	// free. The file stays a few records over the cap until the next append.
	unlock, ok := lockCompaction(path)
	if !ok {
		return nil
	}
	defer unlock()

	// Re-read under the lock: the count above was taken without it.
	lines, err = readLines(path)
	if err != nil || len(lines) <= MaxRecords {
		return err
	}

	// A unique temp file, not a fixed path+".tmp" — two machines compacting the
	// same shared checkout would otherwise write the same file at once.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".journal-*")
	if err != nil {
		return err
	}
	body := strings.Join(lines[len(lines)-MaxRecords:], "\n") + "\n"
	_, werr := tmp.WriteString(body)
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(tmp.Name())
		return werr
	}
	if rerr := os.Rename(tmp.Name(), path); rerr != nil {
		_ = os.Remove(tmp.Name())
		return rerr
	}
	return nil
}

// lockCompaction takes an exclusive marker for one journal file. O_EXCL rather
// than flock so the same code holds on Windows. A lock older than a minute is
// broken: compaction is a read and a rename, and one that has been held longer
// than that belongs to a process that is gone.
//
// Outside the staging repo, beside .sync.lock. Everything inside that tree gets
// committed and pushed, and a lock file caught mid-sync would be published to
// every machine.
func lockCompaction(path string) (func(), bool) {
	staging := filepath.Dir(filepath.Dir(path)) // <staging>/journal/<machine>.jsonl
	lock := filepath.Join(filepath.Dir(staging), "."+filepath.Base(path)+".lock")
	f, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if info, serr := os.Stat(lock); serr == nil && time.Since(info.ModTime()) > time.Minute {
			_ = os.Remove(lock)
			f, err = os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		}
		if err != nil {
			return nil, false
		}
	}
	_ = f.Close()
	return func() { _ = os.Remove(lock) }, true
}

// readLines returns the journal's non-empty lines.
func readLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		if line := sc.Text(); strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	return lines, sc.Err()
}

// fileName maps a machine name to its journal file, keeping the result a single
// safe path segment. The name reaches us from config or the OS hostname, so it
// is not automatically trustworthy as a filename — a name containing a
// separator must not be able to write outside the journal directory.
func fileName(machine string) string {
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '-', r == '_', r == '.':
			return r
		default:
			return '-'
		}
	}, machine)
	safe = strings.Trim(safe, ".-")
	if safe == "" {
		safe = "unknown"
	}
	return safe + ".jsonl"
}

// oneLine collapses text to a single line so one record is always one JSONL
// row's worth of readable text (git error output is routinely multi-line).
func oneLine(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	return strings.Join(strings.Fields(s), " ")
}
