// Package rolloutstore is how a rollout is REPRESENTED in the sync repo. The
// live file in ~/.codex is always plain JSONL and is never touched; this changes
// only the backup's copy.
//
// It exists because of one measurement. On the machine this was written against,
// 59 rollouts total 215 MB — and a single session accounts for 180 MB of it,
// against a median of 0.05 MB. That one file is both over the default per-file
// cap (so it is dropped entirely rather than backed up) and exactly the shape
// that ruins a git repo: append-only, rewritten on every sync, costing its whole
// size in a new blob each time. A session that grows to 180 MB over a hundred
// syncs costs the repo on the order of (size x syncs) / 2 — gigabytes, to record
// a conversation.
//
// So past a threshold a rollout is stored as content-addressed parts plus a
// one-line index. An append rewrites the last part and adds new ones; every
// earlier part keeps its hash, so git already has it. The cost of one more turn
// becomes a chunk rather than a copy.
//
// Two properties everything else depends on:
//
//   - A reader must not care. Open returns something that reads and seeks like
//     the file, whichever representation is on disk, so the header reader, the
//     tail reader, the search and the audit are all unchanged.
//   - The parts are published BEFORE the index. Interrupt it at any point and
//     what is on disk is either the old snapshot or the new one — never an index
//     naming a part that is not there.
package rolloutstore

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ChunkSize is how much of a rollout one part holds.
//
// 4 MiB is a compromise between the two costs. Smaller parts mean a longer index
// and more files; larger parts mean more bytes rewritten for each append. It
// matches clauderig's, which is not laziness: the two repos sit side by side on
// the same machines, and one answer to "how big is a part" is easier to reason
// about than two.
const ChunkSize = 4 << 20

// Suffix names the directory holding a rollout's parts, beside the index.
const Suffix = ".chunks"

// Threshold is the size past which a rollout is stored in parts. Two chunks:
// below that, the index and the directory cost more than they save.
const Threshold = 2 * ChunkSize

// marker is the index's first key. Distinctive enough that a one-line JSON
// document cannot be mistaken for a rollout record, which is the whole basis of
// telling the two representations apart.
const marker = `{"codexrig_chunked_rollout":`

// MaxIndexSize bounds what is read when deciding whether a file is an index. An
// index for a 100 GB rollout is still under a megabyte.
const MaxIndexSize = 16 << 20

// Part is one content-addressed piece.
type Part struct {
	Hash string `json:"sha256"`
	Size int    `json:"size"`
}

// Index is what stands in the repo where the rollout would be.
type Index struct {
	Version int    `json:"codexrig_chunked_rollout"`
	Size    int64  `json:"size"`
	Parts   []Part `json:"parts"`
}

// isIndexFile is IsIndex against a path, reading only the marker's length.
func isIndexFile(p string) (bool, error) {
	f, err := os.Open(p)
	if err != nil {
		return false, err
	}
	defer f.Close()
	head := make([]byte, len(marker))
	n, err := io.ReadFull(f, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return false, err
	}
	return IsIndex(head[:n]), nil
}

// Assemble rebuilds a rollout's bytes from its index, fetching each part by
// hash from wherever the caller keeps them, with the same size and hash checks
// the filesystem reader applies. It exists so a reader that is not the working
// tree — git at a ref, for `peek` — does not hand back the index JSON as if it
// were the conversation.
func Assemble(idx *Index, fetch func(hash string) ([]byte, error)) ([]byte, error) {
	out := make([]byte, 0, idx.Size)
	for _, p := range idx.Parts {
		body, err := fetch(p.Hash)
		if err != nil {
			return nil, fmt.Errorf("rollout part %s: %w", p.Hash[:8], err)
		}
		if len(body) != p.Size {
			return nil, fmt.Errorf("rollout part %s is %d bytes, index says %d", p.Hash[:8], len(body), p.Size)
		}
		sum := sha256.Sum256(body)
		if hex.EncodeToString(sum[:]) != p.Hash {
			return nil, fmt.Errorf("rollout part %s does not match its hash", p.Hash[:8])
		}
		out = append(out, body...)
	}
	if int64(len(out)) != idx.Size {
		return nil, fmt.Errorf("rollout assembled to %d bytes, index says %d", len(out), idx.Size)
	}
	return out, nil
}

// SplitPartPath is PartPath in reverse: the rollout a part belongs to, and its
// hash. ok is false for anything that is not the exact part shape.
func SplitPartPath(partPath string) (rolloutPath, hash string, ok bool) {
	if !IsPartPath(partPath) {
		return "", "", false
	}
	dir, base := path.Split(partPath)
	return strings.TrimSuffix(strings.TrimSuffix(dir, "/"), Suffix), strings.TrimSuffix(base, ".part"), true
}

// References reports whether the index names a part by hash.
func (idx *Index) References(hash string) bool {
	for _, p := range idx.Parts {
		if p.Hash == hash {
			return true
		}
	}
	return false
}

// HashOf is the hash a part with these bytes is named by.
func HashOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// PartPath is where a part lives relative to its rollout: <rollout>.chunks/<hash>.part.
func PartPath(rolloutPath, hash string) string { return rolloutPath + Suffix + "/" + hash + ".part" }

// IsIndex reports whether these bytes START with the index marker.
//
// A prefix test, and the length check is >= rather than >: Open sniffs by
// reading EXACTLY len(marker) bytes, so requiring more than that made every
// chunked rollout read back as its own index. Nothing downstream complained —
// the index is valid JSON and a valid file — it simply returned the wrong
// content.
func IsIndex(b []byte) bool {
	return len(b) >= len(marker) && string(b[:len(marker)]) == marker
}

// Decode parses and VALIDATES an index. Every rule here is a way a corrupt or
// tampered index could otherwise make a reader do something wrong: reconstruct
// the wrong bytes, allocate wildly, or walk out of the parts directory.
func Decode(b []byte) (*Index, error) {
	var idx Index
	if err := json.Unmarshal(b, &idx); err != nil {
		return nil, fmt.Errorf("rollout index: %w", err)
	}
	if idx.Version != 1 {
		return nil, fmt.Errorf("rollout index: unknown version %d", idx.Version)
	}
	var total int64
	for i, p := range idx.Parts {
		if len(p.Hash) != 64 || strings.ToLower(p.Hash) != p.Hash {
			return nil, fmt.Errorf("rollout index: part %d has no lowercase sha256", i)
		}
		if _, err := hex.DecodeString(p.Hash); err != nil {
			return nil, fmt.Errorf("rollout index: part %d hash is not hex", i)
		}
		if p.Size <= 0 || p.Size > ChunkSize {
			return nil, fmt.Errorf("rollout index: part %d has size %d", i, p.Size)
		}
		// Every part but the last is exactly one chunk. Without this, an index
		// could describe the same bytes in many ways and two machines would
		// produce different parts for one file.
		if i < len(idx.Parts)-1 && p.Size != ChunkSize {
			return nil, fmt.Errorf("rollout index: part %d is short but not last", i)
		}
		total += int64(p.Size)
	}
	if total != idx.Size {
		return nil, fmt.Errorf("rollout index: parts total %d, index says %d", total, idx.Size)
	}
	return &idx, nil
}

// IsPartPath reports whether a '/'-separated relative path is one of a chunked
// rollout's parts, so the allowlist reconcile and the contents scan can tell a
// part from a stray file.
func IsPartPath(rel string) bool {
	// The exact shape a chunk has — <rollout>.jsonl.chunks/<sha256>.part, one
	// level down, hex name — and nothing looser. This answer exempts a file
	// from the allowlist and from the audit, on the grounds that its bytes are
	// covered by the index that references it; a .part nested deeper, or named
	// anything but a hash, is covered by nothing and must not ride the
	// exemption.
	dir, base := path.Split(rel)
	if !strings.HasSuffix(strings.TrimSuffix(dir, "/"), ".jsonl"+Suffix) {
		return false
	}
	name, ok := strings.CutSuffix(base, ".part")
	if !ok || len(name) != 64 {
		return false
	}
	for i := 0; i < len(name); i++ {
		if c := name[i]; !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// File is what a reader gets, whichever representation is on disk.
//
// Seek is in the set because the tail reader needs it: reading the end of a
// long conversation without reading the middle is the whole reason these files
// are opened rather than loaded.
type File interface {
	io.ReadCloser
	io.ReaderAt
	io.Seeker
	Stat() (os.FileInfo, error)
}

// Open opens a staged rollout, transparently.
//
// It sniffs rather than trusting the extension, because both representations
// live at the same path — that is what lets every reader stay unaware of which
// one it got.
func Open(p string) (File, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	head := make([]byte, len(marker))
	n, _ := io.ReadFull(f, head)
	if n < len(marker) || !IsIndex(head[:n]) {
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			f.Close()
			return nil, err
		}
		return f, nil
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		f.Close()
		return nil, err
	}
	raw, err := io.ReadAll(io.LimitReader(f, MaxIndexSize))
	closeErr := f.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	idx, err := Decode(raw)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	return &chunked{idx: idx, dir: p + Suffix, info: info}, nil
}

// Stat reports a staged rollout's LOGICAL size and its mtime — the size of the
// conversation, not of the index standing in for it. Every size comparison in
// the sync engine goes through this, so a chunked file compares against its
// source the same way a plain one does.
func Stat(p string) (os.FileInfo, error) {
	f, err := Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return f.Stat()
}

// ReadFile reads a staged rollout whole, whichever representation it is in.
func ReadFile(p string) ([]byte, error) {
	f, err := Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

// Write stores src at dst in parts, and returns whether anything changed.
//
// Parts first, index last. Interrupt it anywhere and dst is either the previous
// snapshot or the new one; an index naming a part that does not exist is not a
// state this can produce.
func Write(dst string, src io.Reader, mtime time.Time) error {
	dir := dst + Suffix
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	idx := Index{Version: 1}
	buf := make([]byte, ChunkSize)
	for {
		n, err := io.ReadFull(src, buf)
		if n > 0 {
			sum := sha256.Sum256(buf[:n])
			hash := hex.EncodeToString(sum[:])
			if werr := writePart(filepath.Join(dir, hash+".part"), buf[:n], hash); werr != nil {
				return werr
			}
			idx.Parts = append(idx.Parts, Part{Hash: hash, Size: n})
			idx.Size += int64(n)
		}
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				break
			}
			return err
		}
	}
	body, err := json.Marshal(idx)
	if err != nil {
		return err
	}
	body = append(body, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(dst), "."+filepath.Base(dst)+".idx-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, 0o644); err != nil {
		return err
	}
	if err := os.Rename(name, dst); err != nil {
		return err
	}
	if err := os.Chtimes(dst, mtime, mtime); err != nil {
		return err
	}
	return sweep(dir, idx)
}

// writePart stores one part, verifying an existing file rather than trusting its
// name. A part is addressed by the hash of its contents, so a file whose bytes
// no longer match its name is corruption — and reusing it because the name
// looked right is how corruption spreads to every machine.
func writePart(p string, data []byte, want string) error {
	if have, err := os.ReadFile(p); err == nil {
		sum := sha256.Sum256(have)
		if hex.EncodeToString(sum[:]) == want {
			return nil
		}
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".part-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, 0o644); err != nil {
		return err
	}
	return os.Rename(name, p)
}

// sweep removes parts the current index does not name — what an earlier version
// of the rollout left behind. Run AFTER the index is in place, so an interrupted
// sweep can only leave extra files, never take away one that is referenced.
func sweep(dir string, idx Index) error {
	keep := make(map[string]bool, len(idx.Parts))
	for _, p := range idx.Parts {
		keep[p.Hash+".part"] = true
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || keep[e.Name()] || !strings.HasSuffix(e.Name(), ".part") {
			continue
		}
		if err := os.Remove(filepath.Join(dir, e.Name())); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// Materialize reconstructs a rollout at dst from whatever is at src.
func Materialize(src, dst string, mode os.FileMode) error {
	f, err := Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), "."+filepath.Base(dst)+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := io.Copy(tmp, f); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, mode); err != nil {
		return err
	}
	return os.Rename(name, dst)
}

// Remove deletes a staged rollout and its parts.
func Remove(p string) error {
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.RemoveAll(p + Suffix)
}

// Convert brings a staged rollout to the representation `chunked` asks for,
// reporting whether it changed anything.
func Convert(p string, chunked bool) (bool, error) {
	// Sniff the head, never the whole file: this is asked of every rollout on
	// every sync, and the rollout that made chunking necessary is 172 MB.
	info, err := os.Stat(p)
	if err != nil {
		return false, err
	}
	isIdx, err := isIndexFile(p)
	if err != nil {
		return false, err
	}
	switch {
	case chunked && !isIdx:
		if info.Size() <= Threshold {
			return false, dropStaleSidecar(p)
		}
		f, err := os.Open(p)
		if err != nil {
			return false, err
		}
		err = Write(p, f, info.ModTime())
		_ = f.Close()
		return err == nil, err
	case !chunked && isIdx:
		if err := Materialize(p, p, 0o644); err != nil {
			return false, err
		}
		if err := os.Chtimes(p, info.ModTime(), info.ModTime()); err != nil {
			return false, err
		}
		return true, os.RemoveAll(p + Suffix)
	}
	// Already chunked and staying chunked: the sidecar is live, leave it.
	if isIdx {
		return false, nil
	}
	return false, dropStaleSidecar(p)
}

// dropStaleSidecar removes a .chunks directory beside a rollout that is plain
// and staying plain. The scrub rewrites a rollout as plain bytes over whatever
// representation was there, and a chunked one left its parts behind — files
// the audit skips (their index vouched for them, except it no longer exists)
// and `git add -A` then publishes. Convert sees every staged rollout, so this is
// the one place that reliably catches it however it happened.
func dropStaleSidecar(p string) error {
	if _, err := os.Lstat(p + Suffix); err != nil {
		return nil
	}
	return os.RemoveAll(p + Suffix)
}

// --- the chunked reader ---------------------------------------------------

type chunked struct {
	idx  *Index
	dir  string
	info os.FileInfo
	pos  int64
	// one part cached, because every reader here is sequential: the header
	// reader walks forward from the start and the tail reader walks forward
	// from an offset. Caching more would hold a rollout in memory, which is
	// what the parts exist to avoid.
	cacheHash string
	cache     []byte
}

func (c *chunked) Stat() (os.FileInfo, error) { return logicalInfo{c.info, c.idx.Size}, nil }

func (c *chunked) Close() error { return nil }

// Seek moves the read cursor within the LOGICAL file — the conversation — not
// within the index standing in for it.
func (c *chunked) Seek(off int64, whence int) (int64, error) {
	var abs int64
	switch whence {
	case io.SeekStart:
		abs = off
	case io.SeekCurrent:
		abs = c.pos + off
	case io.SeekEnd:
		abs = c.idx.Size + off
	default:
		return 0, fmt.Errorf("rolloutstore: bad whence %d", whence)
	}
	if abs < 0 {
		return 0, errors.New("rolloutstore: negative offset")
	}
	c.pos = abs
	return abs, nil
}

func (c *chunked) Read(p []byte) (int, error) {
	n, err := c.ReadAt(p, c.pos)
	c.pos += int64(n)
	return n, err
}

func (c *chunked) ReadAt(p []byte, off int64) (int, error) {
	// io.ReaderAt: a negative offset is an error, not a panic three lines
	// down in a slice expression; and a read that runs off the end returns
	// what it got WITH io.EOF, never a short count and nil.
	if off < 0 {
		return 0, errors.New("rolloutstore: negative offset")
	}
	if off >= c.idx.Size {
		return 0, io.EOF
	}
	read := 0
	for read < len(p) && off < c.idx.Size {
		part, start, err := c.partAt(off)
		if err != nil {
			return read, err
		}
		inPart := off - start
		n := copy(p[read:], part[inPart:])
		read += n
		off += int64(n)
	}
	if read < len(p) {
		return read, io.EOF
	}
	return read, nil
}

// partAt loads the part containing a logical offset, and where that part starts.
func (c *chunked) partAt(off int64) ([]byte, int64, error) {
	var start int64
	for _, p := range c.idx.Parts {
		end := start + int64(p.Size)
		if off < end {
			body, err := c.load(p)
			return body, start, err
		}
		start = end
	}
	return nil, 0, io.EOF
}

// load reads a part and VERIFIES it against the hash it is named by. A silently
// wrong part would reconstruct a conversation that never happened, which is
// worse than an error.
func (c *chunked) load(p Part) ([]byte, error) {
	if c.cacheHash == p.Hash {
		return c.cache, nil
	}
	body, err := os.ReadFile(filepath.Join(c.dir, p.Hash+".part"))
	if err != nil {
		return nil, fmt.Errorf("rollout part %s: %w", p.Hash[:8], err)
	}
	if len(body) != p.Size {
		return nil, fmt.Errorf("rollout part %s is %d bytes, index says %d", p.Hash[:8], len(body), p.Size)
	}
	sum := sha256.Sum256(body)
	if hex.EncodeToString(sum[:]) != p.Hash {
		return nil, fmt.Errorf("rollout part %s does not match its hash", p.Hash[:8])
	}
	c.cacheHash, c.cache = p.Hash, body
	return body, nil
}

// logicalInfo reports the conversation's size rather than the index's.
type logicalInfo struct {
	os.FileInfo
	size int64
}

func (l logicalInfo) Size() int64 { return l.size }

// PartPaths lists a staged rollout's parts, for a caller that needs to know what
// belongs to it.
func PartPaths(p string) ([]string, error) {
	raw, err := os.ReadFile(p)
	if err != nil || !IsIndex(raw) {
		return nil, err //nolint:nilerr // not chunked is not an error
	}
	idx, err := Decode(raw)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(idx.Parts))
	for _, part := range idx.Parts {
		out = append(out, filepath.Join(p+Suffix, part.Hash+".part"))
	}
	sort.Strings(out)
	return out, nil
}
