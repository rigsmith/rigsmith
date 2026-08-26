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
	"unicode"
	"unicode/utf8"
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
	var pruneDir func(name string) bool
	if opts.ChatsOnly {
		keep = isChat
		// Transcripts never live inside Chromium/Electron cache trees, so a
		// chats-only walk prunes those (multi-GB under the Desktop root) instead of
		// stat'ing through them. --all keeps the full walk.
		pruneDir = func(name string) bool { return chatCacheDirs[name] }
	}
	var errs []error
	for _, t := range targets {
		if t.Dir == "" {
			continue
		}
		if info, err := os.Stat(t.Dir); err != nil || !info.IsDir() {
			continue // absent root — nothing to search, not an error
		}
		files, err := listFiles(t.Dir, keep, pruneDir)
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

// ScanFile searches a SINGLE file, for a caller that already knows which files it
// cares about and does not want a walk. `recent` uses it to search only the
// sessions inside the time window, which is a handful of transcripts rather than
// the whole store — the difference between an instant answer and a full scan.
//
// Emitted matches carry the file's base name as Rel and an empty Target; there is
// no root to be relative to.
func ScanFile(path string, opts Options, emit func(Match)) (int, error) {
	if opts.Query == "" {
		return 0, errors.New("empty search query")
	}
	needle := opts.Query
	if !opts.CaseSensitive {
		needle = strings.ToLower(needle)
	}
	t := Target{Dir: filepath.Dir(path)}
	_, matches, err := scanFile(t, path, needle, opts.CaseSensitive, opts.Accept, emit)
	return matches, err
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

	// Size the buffer to the sniff length: Peek(binarySniffBytes) needs a buffer at
	// least that large or it returns ErrBufferFull after the default 4 KiB, missing
	// a NUL past byte 4096 and scanning a binary file as text.
	r := bufio.NewReaderSize(f, binarySniffBytes)
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
			if idx, mlen := findMatch(trimmed, needle, caseSensitive); idx >= 0 {
				if accept != nil && !accept(trimmed) {
					continue // caller rejected this line (e.g. injected boilerplate)
				}
				matches++
				if emit != nil {
					snip, at := window(trimmed, idx, mlen)
					emit(Match{
						Target:   t.Label,
						Path:     path,
						Rel:      rel,
						Line:     lineNo,
						Snippet:  snip,
						MatchAt:  at,
						MatchLen: mlen,
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

// findMatch locates needle in line, returning the match's byte offset and byte
// length WITHIN line, or (-1, 0). For a case-insensitive search needle is already
// lowercased and the match is found rune-by-rune, so the offset indexes the
// original line (a lowercased copy can shift byte positions and its offsets would
// slice the original wrongly) and the length can differ from len(needle) when
// case folding changes a rune's UTF-8 width.
func findMatch(line, needle string, caseSensitive bool) (int, int) {
	if caseSensitive {
		if i := strings.Index(line, needle); i >= 0 {
			return i, len(needle)
		}
		return -1, 0
	}
	return indexFold(line, needle)
}

// indexFold finds needleLower (already lowercased) in s case-insensitively,
// scanning rune starts. It returns the byte offset and byte length of the match
// in s, or (-1, 0).
func indexFold(s, needleLower string) (int, int) {
	if needleLower == "" {
		return -1, 0
	}
	for i := range s { // i iterates rune-start byte offsets
		if n := foldPrefixLen(s[i:], needleLower); n >= 0 {
			return i, n
		}
	}
	return -1, 0
}

// foldPrefixLen returns the byte length of the prefix of s that lowercases
// rune-for-rune to needleLower, or -1 if s has no such prefix. Mirrors the
// per-rune unicode.ToLower that strings.ToLower applies, so it matches the
// case-insensitive semantics without ever building a lowercased copy of s.
func foldPrefixLen(s, needleLower string) int {
	consumed := 0
	for _, nr := range needleLower {
		if consumed >= len(s) {
			return -1
		}
		sr, sz := utf8.DecodeRuneInString(s[consumed:])
		if sr == utf8.RuneError && sz <= 1 {
			return -1
		}
		if unicode.ToLower(sr) != nr {
			return -1
		}
		consumed += sz
	}
	return consumed
}

// window returns line unchanged when it is short, otherwise a snippet padded by
// snippetPad around the match with leading/trailing "…" marking elision. Both
// boundaries are snapped to rune starts (widening the window) so a multibyte
// character is never sliced, which would corrupt the snippet. The second return
// value is the match's byte offset within the returned snippet.
func window(line string, matchIdx, matchLen int) (string, int) {
	if len(line) <= snippetMax {
		return line, matchIdx
	}
	start := matchIdx - snippetPad
	if start < 0 {
		start = 0
	}
	for start > 0 && !utf8.RuneStart(line[start]) {
		start--
	}
	end := matchIdx + matchLen + snippetPad
	if end > len(line) {
		end = len(line)
	}
	for end < len(line) && !utf8.RuneStart(line[end]) {
		end++
	}
	prefix, suffix := "", ""
	if start > 0 {
		prefix = "…"
	}
	if end < len(line) {
		suffix = "…"
	}
	return prefix + line[start:end] + suffix, len(prefix) + (matchIdx - start)
}

// chatCacheDirs are Chromium/Electron storage and cache directory names that live
// under the Desktop root and never hold session transcripts, but can be many
// gigabytes. A chats-only walk prunes them (see Search) so a session lookup
// doesn't stat its way through the browser cache; --all still descends.
var chatCacheDirs = map[string]bool{
	"Cache": true, "Code Cache": true, "GPUCache": true, "DawnCache": true,
	"DawnGraphiteCache": true, "DawnWebGPUCache": true, "GraphiteDawnCache": true,
	"ShaderCache": true, "blob_storage": true, "IndexedDB": true,
	"Local Storage": true, "Session Storage": true, "Shared Dictionary": true,
	"WebStorage": true, "Service Worker": true, "Crashpad": true,
	"VideoDecodeStats": true, "component_crx_cache": true, "extensions_crx_cache": true,
	"Network": true, "shared_proto_db": true, "File System": true, "Partitions": true,
}

// listFiles returns the regular files under root in sorted order. When keep is
// non-nil, only files whose root-relative (slash) path satisfies it are returned;
// when pruneDir is non-nil, a directory whose base name it selects is skipped
// wholesale. Directory read errors mid-walk are skipped (best-effort over a live,
// churning tree); .git inside the synced repo is pruned — it's object soup, not
// chat content.
func listFiles(root string, keep func(rel string) bool, pruneDir func(name string) bool) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable dir/file — skip, don't abort
		}
		if d.IsDir() {
			if d.Name() == ".git" || (pruneDir != nil && pruneDir(d.Name())) {
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
