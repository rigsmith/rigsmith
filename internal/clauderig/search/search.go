// Package search grep's plain text across clauderig's roots — the live ~/.claude
// and Desktop dirs plus the synced staging repo — so you can confirm whether a
// chat (or any config text) still exists anywhere clauderig can see. It is a
// deliberately dumb substring scanner: no regex, no allowlist, no redaction. It
// walks every file under each target, skips the binary ones (caches, leveldb),
// and streams line by line so multi-MB transcripts don't blow up memory.
package search

import (
	"bufio"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// binarySniffBytes is how many leading bytes we read to decide a file is binary
// (a NUL byte ⇒ skip). Transcripts and config are UTF-8 text; caches/leveldb are
// not, and reading only the header keeps the walk cheap over big cache trees.
const binarySniffBytes = 8192

// snippetPad is how many bytes of context we keep on each side of a match when a
// line is longer than snippetMax — transcript lines can be huge (base64 images),
// so we window around the hit instead of dumping the whole line.
const (
	snippetPad = 60
	snippetMax = 200
)

// Target is one root to search: a short Label for output ("cli", "desktop",
// "repo") and the absolute Dir it lives in.
type Target struct {
	Label string
	Dir   string
}

// Match is a single hit: which target, the file (absolute Path and root-relative
// Rel), the 1-based Line, and Snippet — the matched line, windowed to snippetMax
// around the first hit with an ellipsis marking any elision. MatchAt/MatchLen
// locate the hit within Snippet so callers can highlight it.
type Match struct {
	Target   string
	Path     string
	Rel      string
	Line     int
	Snippet  string
	MatchAt  int
	MatchLen int
}

// Stats summarises a search: files scanned, files skipped as binary, and total
// matches emitted.
type Stats struct {
	FilesScanned int
	FilesSkipped int
	Matches      int
}

// Options tune a search. Query is the literal substring to find; empty is an
// error. CaseSensitive defaults off (case-insensitive is friendlier for "did I
// ever say X"). ChatsOnly restricts the walk to session transcripts
// (projects/**.jsonl) — the actual chats — instead of every file, cutting out
// file-history snapshot and cache noise. A nil root or one that doesn't exist is
// skipped, not an error.
type Options struct {
	Query         string
	CaseSensitive bool
	ChatsOnly     bool
	// Accept, when non-nil, is consulted with the full matching line: a false
	// return drops that hit (neither counted nor emitted). Callers use it to skip
	// structurally-uninteresting matches — e.g. injected skill-listing records in a
	// transcript — without the scanner needing to understand the file format.
	Accept func(line string) bool
	// Progress, when non-nil, is called periodically during the walk with the
	// running totals so a caller can show a live status line on a long search.
	Progress func(Stats)
}

// isChat reports whether a root-relative (slash) path is a Claude Code session
// transcript. Matches both the live layout (projects/<slug>/…​.jsonl) and the
// synced-repo layout (cli/projects/<slug>/…​.jsonl), including sub-agent
// transcripts nested under the slug.
func isChat(rel string) bool {
	return strings.HasSuffix(rel, ".jsonl") && strings.Contains(rel, "projects/")
}

// Search walks each target's files and calls emit for every line containing the
// query. Targets are searched in order; within a target, files are visited in
// sorted (deterministic) path order. Per-file read errors are collected and
// returned as a joined error at the end — a single unreadable file never aborts
// the whole search. emit may be nil (counts only).
func Search(targets []Target, opts Options, emit func(Match)) (Stats, error) {
	var stats Stats
	if opts.Query == "" {
		return stats, errors.New("empty search query")
	}
	needle := opts.Query
	if !opts.CaseSensitive {
		needle = strings.ToLower(needle)
	}
	var keep func(string) bool
	if opts.ChatsOnly {
		keep = isChat
	}
	var errs []error
	for _, t := range targets {
		if t.Dir == "" {
			continue
		}
		if info, err := os.Stat(t.Dir); err != nil || !info.IsDir() {
			continue // absent root — nothing to search, not an error
		}
		files, err := listFiles(t.Dir, keep)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, path := range files {
			scanned, matches, err := scanFile(t, path, needle, opts.CaseSensitive, opts.Accept, emit)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			if scanned {
				stats.FilesScanned++
			} else {
				stats.FilesSkipped++
			}
			stats.Matches += matches
			// Throttled progress: every 64 files is frequent enough to feel live
			// without flooding the terminal on a big tree.
			if opts.Progress != nil && (stats.FilesScanned+stats.FilesSkipped)%64 == 0 {
				opts.Progress(stats)
			}
		}
	}
	return stats, errors.Join(errs...)
}

// scanFile reads one file line by line, emitting a Match per hit. It returns
// whether the file was actually scanned (false ⇒ skipped as binary) and the
// number of matches. A NUL byte in the header marks the file binary and skips it.
func scanFile(t Target, path, needle string, caseSensitive bool, accept func(string) bool, emit func(Match)) (scanned bool, matches int, err error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) || os.IsPermission(err) {
			return false, 0, nil // vanished mid-walk / unreadable — skip quietly
		}
		return false, 0, err
	}
	defer f.Close()

	r := bufio.NewReader(f)
	if isBinary(r) {
		return false, 0, nil
	}

	rel, _ := filepath.Rel(t.Dir, path)
	rel = filepath.ToSlash(rel)
	lineNo := 0
	for {
		line, rerr := r.ReadString('\n')
		if len(line) > 0 {
			lineNo++
			trimmed := strings.TrimRight(line, "\r\n")
			hay := trimmed
			if !caseSensitive {
				hay = strings.ToLower(trimmed)
			}
			if idx := strings.Index(hay, needle); idx >= 0 {
				if accept != nil && !accept(trimmed) {
					continue // caller rejected this line (e.g. injected boilerplate)
				}
				matches++
				if emit != nil {
					snip, at := window(trimmed, idx, len(needle))
					emit(Match{
						Target:   t.Label,
						Path:     path,
						Rel:      rel,
						Line:     lineNo,
						Snippet:  snip,
						MatchAt:  at,
						MatchLen: len(needle),
					})
				}
			}
		}
		if rerr != nil {
			if rerr != io.EOF {
				return true, matches, rerr
			}
			break
		}
	}
	return true, matches, nil
}

// isBinary peeks the reader's first bytes (without consuming them) and reports
// whether they contain a NUL — our cheap "this isn't text" test.
func isBinary(r *bufio.Reader) bool {
	head, _ := r.Peek(binarySniffBytes)
	for _, b := range head {
		if b == 0 {
			return true
		}
	}
	return false
}

// window returns line unchanged when it is short, otherwise a snippet padded by
// snippetPad around the match with leading/trailing "…" marking elision. The
// second return value is the match's byte offset within the returned snippet.
func window(line string, matchIdx, matchLen int) (string, int) {
	if len(line) <= snippetMax {
		return line, matchIdx
	}
	start := matchIdx - snippetPad
	prefix := ""
	if start > 0 {
		prefix = "…"
	} else {
		start = 0
	}
	end := matchIdx + matchLen + snippetPad
	suffix := ""
	if end < len(line) {
		suffix = "…"
	} else {
		end = len(line)
	}
	return prefix + line[start:end] + suffix, len(prefix) + (matchIdx - start)
}

// listFiles returns the regular files under root in sorted order. When keep is
// non-nil, only files whose root-relative (slash) path satisfies it are returned.
// Directory read errors mid-walk are skipped (best-effort over a live, churning
// tree); .git inside the synced repo is pruned — it's object soup, not chat
// content.
func listFiles(root string, keep func(rel string) bool) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable dir/file — skip, don't abort
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if keep != nil {
			rel, _ := filepath.Rel(root, p)
			if !keep(filepath.ToSlash(rel)) {
				return nil
			}
		}
		out = append(out, p)
		return nil
	})
	sort.Strings(out)
	return out, err
}
