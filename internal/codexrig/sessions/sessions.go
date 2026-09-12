// Package sessions assembles what is known about Codex sessions from every
// store that holds one — this machine's live Codex home, and the synced repo
// with every machine's.
//
// Codex's date sharding looks like leverage for a time-windowed listing, and it
// is — but only in one direction. The shard is when a session STARTED, and Codex
// appends to the same rollout on resume, so an old directory can hold a
// conversation that was active this morning. See shardOutOfWindow.
package sessions

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rigsmith/rigsmith/internal/codexrig/rollout"
)

// Store labels where a copy of a session was found.
const (
	Live = "live" // this machine's Codex home
	Repo = "repo" // the synced repo, holding every machine's
)

// Target is one place to look.
type Target struct {
	Label string
	Dir   string
}

// Row is one session, assembled from wherever it was found.
type Row struct {
	ID    string    `json:"id"`
	When  time.Time `json:"when"`
	Cwd   string    `json:"cwd,omitempty"`
	Title string    `json:"title,omitempty"`
	// Last is the most recent thing said, in either direction — usually more
	// use than the title for telling two sessions in one repo apart.
	Last    string `json:"last,omitempty"`
	Branch  string `json:"branch,omitempty"`
	Model   string `json:"model,omitempty"`
	Source  string `json:"source,omitempty"`
	Version string `json:"cliVersion,omitempty"`

	Stores []string `json:"stores"`
	Path   string   `json:"path"`
	// Resumable is true only for a session in this machine's own Codex home:
	// `codex resume` reads from there, so a repo-only copy has to be restored
	// before it can be opened.
	Resumable bool `json:"resumable"`

	Matches int    `json:"matches,omitempty"`
	Snippet string `json:"snippet,omitempty"`
}

// Options select and filter.
type Options struct {
	Targets []Target
	// Since and Until bound by the session's own timestamps, which always come
	// from the records rather than from the shard or the file's mtime. See
	// shardOutOfWindow for what the directory layout can and cannot prune.
	Since time.Time
	Until time.Time
	// Cwd matches the working directory, case-insensitively.
	Cwd string
	// Text matches the title, last message, cwd, branch or id — cheap, no file
	// body opened.
	Text string
	// Content searches inside the conversation. Expensive, so it runs only
	// where Text did not already match.
	Content       string
	CaseSensitive bool
	Limit         int
}

// Report is what a listing had to work with.
type Report struct {
	Read    int
	Skipped int
	Total   int
}

// List assembles the sessions each target holds.
func List(opts Options) ([]Row, Report) {
	var rep Report
	byID := map[string]*Row{}

	for _, t := range opts.Targets {
		for _, f := range walkRollouts(t.Dir, opts) {
			id := rollout.IDFromRolloutRel(f.rel)
			if id == "" {
				continue
			}
			row := byID[id]
			if row == nil {
				row = &Row{ID: id}
				byID[id] = row
			}
			if !contains(row.Stores, t.Label) {
				row.Stores = append(row.Stores, t.Label)
			}
			if t.Label == Live {
				row.Resumable = true
			}
			// The live copy wins as the one to read: it is at least as complete
			// as any snapshot of it, and it is the one `codex resume` opens.
			if row.Path == "" || t.Label == Live {
				row.Path = f.abs
			}
		}
	}

	rows := make([]Row, 0, len(byID))
	for _, row := range byID {
		if !hydrate(row) {
			rep.Skipped++
			continue
		}
		rep.Read++
		if !keep(*row, opts) {
			continue
		}
		if opts.Content != "" && !matchesText(*row, opts.Content, opts.CaseSensitive) {
			n, snip := scanFile(row.Path, opts.Content, opts.CaseSensitive)
			if n == 0 {
				continue
			}
			row.Matches, row.Snippet = n, snip
		}
		rows = append(rows, *row)
	}

	sort.Slice(rows, func(i, j int) bool {
		if !rows[i].When.Equal(rows[j].When) {
			return rows[i].When.After(rows[j].When)
		}
		return rows[i].ID < rows[j].ID
	})
	rep.Total = len(rows)
	if opts.Limit > 0 && len(rows) > opts.Limit {
		rows = rows[:opts.Limit]
	}
	return rows, rep
}

type found struct {
	rel string
	abs string
}

// walkRollouts lists the rollouts under a directory, pruning date shards that
// cannot hold anything in the window.
func walkRollouts(dir string, opts Options) []found {
	var out []found
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return nil //nolint:nilerr // an unreadable corner is not a reason to list nothing
		}
		rel, rerr := filepath.Rel(dir, p)
		if rerr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if shardOutOfWindow(rel, opts) {
				return fs.SkipDir
			}
			return nil
		}
		if rollout.IsRolloutRel(rel) {
			out = append(out, found{rel: rel, abs: p})
		}
		return nil
	})
	return out
}

// shardOutOfWindow reports whether a sessions/YYYY/MM/DD directory can be
// skipped without opening anything inside it.
//
// Only the LATE side is prunable, and the asymmetry is the whole point.
//
// The shard names when a session STARTED, and Codex APPENDS to the same rollout
// when you resume it — one on the machine this was written against spans eight
// calendar days and never leaves its original directory. So a shard older than
// the `since` bound may be full of records inside the window, and pruning it
// would silently hide exactly the long-running sessions a person is most likely
// to be looking for. (This was written the other way first, with a day of slack
// either side. A day is not eight.)
//
// The `until` side is safe, because a session cannot have records before it
// started: a shard dated after the bound holds nothing that belongs in the
// window. One day of slack covers a session opened just before midnight in a
// timezone the shard does not record.
//
// Mtime is not a substitute for the missing half. A restore re-dates whole
// trees, so a file's timestamp says when it arrived here, not when the
// conversation happened.
func shardOutOfWindow(rel string, opts Options) bool {
	if opts.Until.IsZero() {
		return false
	}
	day, ok := rollout.DateOf(rel + "/rollout-0000-00-00T00-00-00-00000000-0000-0000-0000-000000000000.jsonl")
	if !ok {
		return false
	}
	return day.After(opts.Until.AddDate(0, 0, 1))
}

// hydrate fills a row from its file. False means the file said nothing usable,
// which is a skip rather than a blank row.
func hydrate(row *Row) bool {
	meta, ok, _ := rollout.ReadMeta(row.Path)
	if ok {
		row.Cwd, row.Branch, row.Source, row.Version = meta.Cwd, meta.Branch, meta.Source, meta.CLIVersion
		row.When = meta.At
	}
	if act, ok := rollout.LastActivity(row.Path); ok {
		if !act.At.IsZero() {
			row.When = act.At
		}
		row.Model = act.Model
		row.Last = rollout.Tidy(firstNonEmpty(act.LastPrompt, act.LastReply))
	}
	row.Title = rollout.FirstPrompt(row.Path)
	if row.Title == "" {
		row.Title = row.Last
	}
	return !row.When.IsZero() || row.Title != ""
}

func keep(row Row, opts Options) bool {
	if !opts.Since.IsZero() && row.When.Before(opts.Since) {
		return false
	}
	if !opts.Until.IsZero() && row.When.After(opts.Until) {
		return false
	}
	if opts.Cwd != "" && !strings.Contains(strings.ToLower(row.Cwd), strings.ToLower(opts.Cwd)) {
		return false
	}
	if opts.Text != "" && !matchesText(row, opts.Text, opts.CaseSensitive) {
		return false
	}
	return true
}

func matchesText(row Row, needle string, caseSensitive bool) bool {
	hay := strings.Join([]string{row.Title, row.Last, row.Cwd, row.Branch, row.ID, row.Model}, "\x00")
	if caseSensitive {
		return strings.Contains(hay, needle)
	}
	return strings.Contains(strings.ToLower(hay), strings.ToLower(needle))
}

// snippetMax bounds what a match shows, and snippetPad how much sits either
// side of the hit when the line is longer.
const (
	snippetMax = 200
	snippetPad = 60
)

// scanFile counts matches inside a rollout's conversation and returns the first
// one in context.
//
// It reads the file, which is why it runs only after the cheap field match has
// missed: a listing that opened every rollout would take seconds per screen.
func scanFile(path, needle string, caseSensitive bool) (int, string) {
	f, err := os.Open(path)
	if err != nil {
		return 0, ""
	}
	defer f.Close()

	count := 0
	snippet := ""
	sc := newScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if !rollout.IsConversationLine(line) {
			continue
		}
		_, text, ok := rollout.MessageText(line)
		if !ok || text == "" {
			continue
		}
		hay := text
		if !caseSensitive {
			hay = strings.ToLower(hay)
			needle = strings.ToLower(needle)
		}
		i := strings.Index(hay, needle)
		if i < 0 {
			continue
		}
		count++
		if snippet == "" {
			snippet = window(text, i, len(needle))
		}
	}
	return count, snippet
}

// window cuts a readable piece around a hit, on rune boundaries so a multi-byte
// character is never split in half.
func window(text string, at, length int) string {
	text = strings.Join(strings.Fields(text), " ")
	if len(text) <= snippetMax {
		return text
	}
	start := at - snippetPad
	if start < 0 {
		start = 0
	}
	end := at + length + snippetPad
	if end > len(text) {
		end = len(text)
	}
	for start > 0 && !isRuneStart(text[start]) {
		start--
	}
	for end < len(text) && !isRuneStart(text[end]) {
		end++
	}
	out := text[start:end]
	if start > 0 {
		out = "…" + out
	}
	if end < len(text) {
		out += "…"
	}
	return out
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
