// Package transcript reads native transcripts and versioned chunked snapshots.
// Live Claude files are always native JSONL. Chunking changes only the staged backup representation.
package transcript

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const ChunkSize = 4 << 20
const marker = `{"clauderig_chunked_transcript":`
const Suffix = ".chunks"
const maxIndex = 16 << 20

type Index struct {
	Version int    `json:"clauderig_chunked_transcript"`
	Size    int64  `json:"size"`
	Parts   []Part `json:"parts"`
}
type Part struct {
	Hash string `json:"sha256"`
	Size int    `json:"size"`
}

func IsIndex(b []byte) bool { return bytes.HasPrefix(bytes.TrimSpace(b), []byte(marker)) }
func IsPartPath(p string) bool {
	for _, s := range strings.Split(filepath.ToSlash(p), "/") {
		if strings.HasSuffix(s, ".jsonl"+Suffix) {
			return true
		}
	}
	return false
}
func Decode(b []byte) (*Index, error) {
	if !IsIndex(b) {
		return nil, nil
	}
	var idx Index
	if len(b) > maxIndex {
		return nil, fmt.Errorf("chunk index too large")
	}
	if err := json.Unmarshal(b, &idx); err != nil {
		return nil, fmt.Errorf("invalid transcript index: %w", err)
	}
	if idx.Version != 1 {
		return nil, fmt.Errorf("unsupported transcript storage version %d; upgrade clauderig", idx.Version)
	}
	var size int64
	for i, p := range idx.Parts {
		h, e := hex.DecodeString(p.Hash)
		if e != nil || len(h) != 32 || p.Hash != strings.ToLower(p.Hash) || p.Size <= 0 || p.Size > ChunkSize || (i < len(idx.Parts)-1 && p.Size != ChunkSize) {
			return nil, fmt.Errorf("invalid transcript chunk %d", i)
		}
		size += int64(p.Size)
	}
	if size != idx.Size || size < 0 {
		return nil, fmt.Errorf("transcript size does not match chunks")
	}
	return &idx, nil
}

// File is the common random-access surface needed by header/tail readers.
type File interface {
	io.Reader
	io.ReaderAt
	io.Closer
	Stat() (os.FileInfo, error)
}

type logicalInfo struct {
	os.FileInfo
	size int64
}

func (i logicalInfo) Size() int64 { return i.size }

type chunkFile struct {
	mu         sync.Mutex
	index      *Index
	info       os.FileInfo
	load       func(string) ([]byte, error)
	offset     int64
	cachedHash string
	cached     []byte
}

func (f *chunkFile) Stat() (os.FileInfo, error) { return logicalInfo{f.info, f.index.Size}, nil }
func (f *chunkFile) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cached = nil
	f.cachedHash = ""
	return nil
}
func (f *chunkFile) Read(b []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n, e := f.readAt(b, f.offset)
	f.offset += int64(n)
	return n, e
}
func (f *chunkFile) ReadAt(b []byte, off int64) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.readAt(b, off)
}
func (f *chunkFile) readAt(b []byte, off int64) (int, error) {
	if off < 0 {
		return 0, fmt.Errorf("negative transcript offset")
	}
	if len(b) == 0 {
		return 0, nil
	}
	n := 0
	for n < len(b) {
		if off >= f.index.Size {
			return n, io.EOF
		}
		p := f.index.Parts[off/ChunkSize]
		if p.Hash != f.cachedHash || len(f.cached) != p.Size {
			data, e := f.load(p.Hash)
			if e != nil {
				return n, e
			}
			if len(data) != p.Size || fmt.Sprintf("%x", sha256.Sum256(data)) != p.Hash {
				return n, fmt.Errorf("corrupt transcript chunk %s", p.Hash)
			}
			f.cachedHash, f.cached = p.Hash, data
		}
		copied := copy(b[n:], f.cached[off%ChunkSize:])
		n += copied
		off += int64(copied)
	}
	return n, nil
}

func Open(path string) (File, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	if !strings.HasSuffix(path, ".jsonl") {
		return f, nil
	}
	var head [512]byte
	n, e := f.ReadAt(head[:], 0)
	if e != nil && e != io.EOF {
		f.Close()
		return nil, e
	}
	if !IsIndex(head[:n]) {
		return f, nil
	}
	info, e := f.Stat()
	if e != nil {
		f.Close()
		return nil, e
	}
	b, e := io.ReadAll(io.LimitReader(f, maxIndex+1))
	f.Close()
	if e != nil {
		return nil, e
	}
	idx, e := Decode(b)
	if e != nil {
		return nil, e
	}
	load := func(hash string) ([]byte, error) {
		dir := path + Suffix
		if st, e := os.Lstat(dir); e != nil {
			return nil, e
		} else if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("invalid chunk directory")
		}
		name := filepath.Join(dir, hash+".part")
		st, e := os.Lstat(name)
		if e != nil {
			return nil, e
		}
		if !st.Mode().IsRegular() || st.Size() > ChunkSize {
			return nil, fmt.Errorf("invalid chunk file")
		}
		return os.ReadFile(name)
	}
	return &chunkFile{index: idx, info: info, load: load}, nil
}
func Stat(path string) (os.FileInfo, error) {
	f, e := Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	return f.Stat()
}
func ReadFile(path string) ([]byte, error) {
	f, e := Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	return io.ReadAll(f)
}

// ReadStored resolves an index through a caller's object-store reader. Passing a
// prefix limit avoids loading an entire conversation just to index its title.
func ReadStored(path string, b []byte, load func(string) ([]byte, error), limit int64) ([]byte, error) {
	idx, e := Decode(b)
	if e != nil {
		return nil, e
	}
	if idx == nil {
		if limit > 0 && int64(len(b)) > limit {
			b = b[:limit]
		}
		return b, nil
	}
	f := &chunkFile{index: idx, load: func(h string) ([]byte, error) { return load(filepath.ToSlash(path) + Suffix + "/" + h + ".part") }}
	var r io.Reader = f
	if limit > 0 {
		r = io.LimitReader(f, limit)
	}
	return io.ReadAll(r)
}

// Write publishes chunks before the index, so interruption leaves the old
// snapshot readable. Content-addressed sealed chunks are reused on append.
func Write(dst string, src io.Reader, mtime time.Time) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	dir := dst + Suffix
	if st, e := os.Lstat(dir); e == nil {
		if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("invalid chunk directory")
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	idx := Index{Version: 1, Parts: []Part{}}
	buf := make([]byte, ChunkSize)
	for {
		n, err := io.ReadFull(src, buf)
		if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
			return err
		}
		if n > 0 {
			data := buf[:n]
			hash := fmt.Sprintf("%x", sha256.Sum256(data))
			p := filepath.Join(dir, hash+".part")
			// Validate existing bytes too: a damaged blob must not be reused by name.
			var existing []byte
			var e error
			if st, err := os.Lstat(p); err == nil && st.Mode().IsRegular() && st.Size() <= ChunkSize {
				existing, e = os.ReadFile(p)
			}
			if e != nil || !bytes.Equal(existing, data) {
				if e = atomicWrite(p, bytes.NewReader(data), mtime, 0o644); e != nil {
					return e
				}
			}
			idx.Parts = append(idx.Parts, Part{hash, n})
			idx.Size += int64(n)
		}
		if err != nil {
			break
		}
	}
	b, err := json.Marshal(idx)
	if err != nil {
		return err
	}
	if len(b)+1 > maxIndex {
		return fmt.Errorf("chunk index too large")
	}
	return atomicWrite(dst, bytes.NewReader(append(b, '\n')), mtime, 0o644)
}

// Materialize verifies chunks as it copies and atomically replaces only after
// successful reassembly. Corrupt input cannot truncate an existing live file.
func Materialize(src, dst string, mode os.FileMode) error {
	f, e := Open(src)
	if e != nil {
		return e
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil {
		return e
	}
	return atomicWrite(dst, f, st.ModTime(), mode)
}
func atomicWrite(dst string, r io.Reader, mtime time.Time, mode os.FileMode) (err error) {
	if err = os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(dst), ".clauderig-chunk-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer func() { f.Close(); os.Remove(name) }()
	if _, err = io.Copy(f, r); err != nil {
		return err
	}
	if err = f.Chmod(mode); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Chtimes(name, mtime, mtime); err != nil {
		return err
	}
	return os.Rename(name, dst)
}

// Clean removes unreferenced chunk objects only after an index is complete.
// Their mtimes follow the owning snapshot so retention never ages sealed chunks
// out of a still-active conversation. Orphan directories are also removed.
func Clean(root string) error {
	return filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
		if os.IsNotExist(e) {
			return nil
		}
		if e != nil {
			return e
		}
		if !d.IsDir() || !strings.HasSuffix(d.Name(), ".jsonl"+Suffix) {
			return nil
		}
		owner := strings.TrimSuffix(p, Suffix)
		b, e := os.ReadFile(owner)
		if os.IsNotExist(e) {
			if e = os.RemoveAll(p); e != nil {
				return e
			}
			return filepath.SkipDir
		}
		if e != nil {
			return e
		}
		idx, e := Decode(b)
		if e != nil {
			return e
		}
		if idx == nil {
			if e = os.RemoveAll(p); e != nil {
				return e
			}
			return filepath.SkipDir
		}
		st, e := os.Stat(owner)
		if e != nil {
			return e
		}
		keep := map[string]bool{}
		for _, part := range idx.Parts {
			keep[part.Hash+".part"] = true
		}
		entries, e := os.ReadDir(p)
		if e != nil {
			return e
		}
		for _, entry := range entries {
			target := filepath.Join(p, entry.Name())
			if !keep[entry.Name()] {
				if e = os.RemoveAll(target); e != nil {
					return e
				}
			} else if !entry.Type().IsRegular() {
				return fmt.Errorf("invalid chunk file: %s", target)
			} else if e = os.Chtimes(target, st.ModTime(), st.ModTime()); e != nil {
				return e
			}
		}
		return filepath.SkipDir
	})
}

const StorageFile = "clauderig-storage.json"

// Enabled is a repository-level setting, so a second upgraded machine adopts
// chunking without requiring its local config to be edited as well.
func Enabled(root string) (bool, error) {
	b, e := os.ReadFile(filepath.Join(root, StorageFile))
	if os.IsNotExist(e) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	var mode struct {
		Version int  `json:"version"`
		Chunked bool `json:"chunkedTranscripts"`
	}
	if e = json.Unmarshal(b, &mode); e != nil {
		return false, e
	}
	if mode.Version != 1 {
		return false, fmt.Errorf("unsupported clauderig storage version %d; upgrade clauderig", mode.Version)
	}
	return mode.Chunked, nil
}

// ConvertTree supports migration in either direction, including transcripts
// contributed by other machines. Individual files publish atomically; rerunning
// completes an interrupted conversion without changing native transcript bytes.
func ConvertTree(root string, enabled bool) error {
	if err := filepath.WalkDir(filepath.Join(root, "cli", "projects"), func(p string, d os.DirEntry, e error) error {
		if os.IsNotExist(e) {
			return nil
		}
		if e != nil {
			return e
		}
		if d.IsDir() {
			if IsPartPath(p) {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("invalid staged file: %s", p)
		}
		if !strings.HasSuffix(p, ".jsonl") {
			return nil
		}
		f, e := Open(p)
		if e != nil {
			return e
		}
		defer f.Close()
		st, e := f.Stat()
		if e != nil {
			return e
		}
		_, packed := f.(*chunkFile)
		if enabled && !packed && st.Size() > 2*ChunkSize {
			return Write(p, f, st.ModTime())
		}
		if !enabled && packed {
			return Materialize(p, p, st.Mode().Perm())
		}
		return nil
	}); err != nil {
		return err
	}
	if err := Clean(filepath.Join(root, "cli", "projects")); err != nil {
		return err
	}
	// Avoid changing plain legacy repos unless they are actually migrated.
	if !enabled {
		if _, e := os.Stat(filepath.Join(root, StorageFile)); os.IsNotExist(e) {
			return nil
		}
	}
	data := fmt.Sprintf("{\"version\":1,\"chunkedTranscripts\":%t}\n", enabled)
	return atomicWrite(filepath.Join(root, StorageFile), strings.NewReader(data), time.Now(), 0o644)
}

// CheckNativeLimit refuses a rollback that would discard a chunked backup under
// the native-file cap. Call before changing the tree so the user can keep chunking
// or choose a suitable cap without losing the existing snapshot.
func CheckNativeLimit(root string, limit int64) error {
	if limit <= 0 {
		return nil
	}
	return filepath.WalkDir(filepath.Join(root, "cli", "projects"), func(p string, d os.DirEntry, err error) error {
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if d.IsDir() {
			if IsPartPath(p) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".jsonl") {
			return nil
		}
		f, err := Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		if packed, ok := f.(*chunkFile); ok && packed.index.Size > limit {
			return fmt.Errorf("cannot disable chunking: %s is %d bytes, above retention.maxFileBytes (%d); keep chunking or increase the native-file cap", p, packed.index.Size, limit)
		}
		return nil
	})
}
