// Package artifact seals immutable private capture archives. It never executes
// Git, chooses vendor policies, or overwrites a destination checkout.
package artifact

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rigsmith/rigsmith/internal/agentrig/durable"
	"github.com/rigsmith/rigsmith/internal/agentrig/storelock"
)

const magic = "RIGCAP01"
const headerSize = int64(len(magic) + 64)
const DefaultMaxBytes int64 = 32 << 30

var ErrInvalid = errors.New("invalid capture artifact")
var ErrTooLarge = errors.New("capture artifact exceeds size limit")
var ErrStoreFull = errors.New("artifact store limit reached; retained artifacts were preserved")

// Store lives outside every backup and source root. Its directory and ancestors
// must stay stable during operations. MaxBytes bounds one archive (zero uses
// 32 GiB); capacity cleanup/replay horizons are caller responsibilities.
// Direct .durable-* names are reserved for disposable archive-write scratch;
// callers must never place unrelated data in that namespace (see cleanup).
type Store struct {
	Dir      string
	MaxBytes int64
	// MaxStoredBytes bounds new sealed archives in this directory, excluding
	// temporary files and substores. Zero disables the aggregate admission limit.
	// All writers sharing the store must use the same configured limit.
	// This is runtime admission policy, not capture identity: callers may change
	// it between attempts to repair capacity exhaustion without replacing work.
	MaxStoredBytes int64
	reflush        func(context.Context, string) error // nil uses durable.Rewrite; per-store fault injection
}

func (s Store) limit() int64 {
	if s.MaxBytes == 0 {
		return DefaultMaxBytes
	}
	return s.MaxBytes
}
func digest(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }

// Key hashes a caller's immutable binding and sealed event membership. Never
// include mutable phase/retry fields; the same capture must keep the same key.
func Key(identity []byte) string { return digest(identity) }
func validHash(v string) bool {
	b, err := hex.DecodeString(v)
	return err == nil && len(b) == sha256.Size && v == strings.ToLower(v)
}
func (s Store) path(key string) (string, error) {
	if !validHash(key) || !filepath.IsAbs(s.Dir) || s.limit() < headerSize+sha256.Size || s.MaxStoredBytes < 0 {
		return "", ErrInvalid
	}
	return filepath.Join(s.Dir, key+".capture"), nil
}

// Exists reports whether the exact artifact path is present, including corrupt
// files and nonregular entries. It does not verify or confirm durability; callers
// must still use Build/Verify before accepting any saved artifact. Work folders
// left by a failed build do not count as an artifact.
func (s Store) Exists(key string) (bool, error) {
	path, err := s.path(key)
	if err != nil {
		return false, err
	}
	_, err = os.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil, err
}

// Metadata retains the adapter's immutable seed reference for later publication.
// BaseReference is credential-free (for Git, the source staging HEAD or empty).
type Metadata struct {
	BaseReference string
	// SeedReference names a separately durable seed artifact, when the adapter
	// requires retained ancestry. It is private metadata, never an extracted file.
	SeedReference string `json:",omitempty"`
}

// Build reuses and reflushes an existing valid artifact without invoking build.
// Otherwise build receives an empty private workspace to fill with audited bytes.
// Build holds artifact-store ownership; build must use its original context to
// acquire any staging lease, never a borrowed artifact-store context. On success
// the returned reference binds both the capture key and every archive byte.
func (s Store) Build(ctx context.Context, key string, build func(context.Context, string) error) (string, error) {
	if build == nil {
		return "", ErrInvalid
	}
	return s.BuildWithMetadata(ctx, key, func(ctx context.Context, tree string, _ *Metadata) error { return build(ctx, tree) })
}

// BuildWithMetadata is Build with an adapter-supplied seed reference sealed in
// the archive header. The reference is never extracted as a backup file.
func (s Store) BuildWithMetadata(ctx context.Context, key string, build func(context.Context, string, *Metadata) error) (string, error) {
	path, err := s.path(key)
	if err != nil {
		return "", err
	}
	if build == nil {
		return "", ErrInvalid
	}
	if err = os.MkdirAll(s.Dir, 0700); err != nil {
		return "", err
	}
	st, err := os.Lstat(s.Dir)
	if err != nil {
		return "", err
	}
	if !st.IsDir() {
		return "", ErrInvalid
	}
	_, release, err := storelock.Acquire(ctx, s.Dir, 15*time.Second)
	if err != nil {
		return "", err
	}
	defer release()
	if ref, err := s.inspect(ctx, key, ""); err == nil {
		reflush := s.reflush
		if reflush == nil {
			reflush = durable.Rewrite
		}
		if err = reflush(ctx, path); err != nil {
			return "", err
		}
		return ref, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	archiveLimit, capacityError := s.limit(), ErrTooLarge
	if s.MaxStoredBytes > 0 {
		usage, _, err := s.inventory(ctx)
		if err != nil {
			return "", err
		}
		remaining := max(int64(0), s.MaxStoredBytes-usage.StoredBytes)
		if remaining < headerSize+sha256.Size {
			return "", ErrStoreFull
		}
		if remaining < archiveLimit {
			archiveLimit, capacityError = remaining, ErrStoreFull
		}
	}
	work, err := os.MkdirTemp(s.Dir, ".capture-work-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(work)
	tree := filepath.Join(work, "tree")
	if err = os.Mkdir(tree, 0700); err != nil {
		return "", err
	}
	meta := Metadata{}
	if err = build(ctx, tree, &meta); err != nil {
		return "", err
	}
	if len(meta.BaseReference) > 4096 || len(meta.SeedReference) > 4096 {
		return "", ErrInvalid
	}
	metadata, err := json.Marshal(meta)
	if err != nil {
		return "", err
	}
	var sum string
	err = durable.Write(ctx, path, func(f *os.File) error {
		h := sha256.New()
		out := &limitedWriter{w: io.MultiWriter(f, h), left: archiveLimit - sha256.Size, limitError: capacityError}
		if _, err := io.WriteString(out, magic+key); err != nil {
			return err
		}
		var length [4]byte
		binary.BigEndian.PutUint32(length[:], uint32(len(metadata)))
		if _, err := out.Write(length[:]); err != nil {
			return err
		}
		if _, err := out.Write(metadata); err != nil {
			return err
		}
		if err := writeTree(ctx, out, tree); err != nil {
			return err
		}
		sum = hex.EncodeToString(h.Sum(nil))
		_, err := f.Write(h.Sum(nil))
		return err
	})
	if err != nil {
		return "", err
	}
	return key + ":" + sum, nil
}

// Verify checks the archive checksum and key. This read does not acknowledge an
// uncertain Build; retry Build itself to reflush before marking capture complete.
func (s Store) Verify(ctx context.Context, ref string) error {
	key, sum, ok := strings.Cut(ref, ":")
	if !ok || !validHash(sum) {
		return ErrInvalid
	}
	_, err := s.inspect(ctx, key, sum)
	return err
}
func (s Store) inspect(ctx context.Context, key, want string) (string, error) {
	f, err := s.open(key)
	if err != nil {
		return "", err
	}
	defer f.Close()
	sum, err := s.check(ctx, f, key)
	if err != nil {
		return "", err
	}
	if want != "" && sum != want {
		return "", ErrInvalid
	}
	return key + ":" + sum, nil
}
func (s Store) open(key string) (*os.File, error) {
	path, err := s.path(key)
	if err != nil {
		return nil, err
	}
	st, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, ErrInvalid
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	actual, err := f.Stat()
	if err != nil || !os.SameFile(st, actual) {
		f.Close()
		return nil, ErrInvalid
	}
	return f, nil
}
func (s Store) check(ctx context.Context, f *os.File, key string) (string, error) {
	st, err := f.Stat()
	if err != nil {
		return "", err
	}
	if st.Size() > s.limit() {
		return "", ErrTooLarge
	}
	if st.Size() < headerSize+sha256.Size {
		return "", ErrInvalid
	}
	header := make([]byte, headerSize)
	if _, err = io.ReadFull(f, header); err != nil {
		return "", err
	}
	if string(header) != magic+key {
		return "", ErrInvalid
	}
	h := sha256.New()
	h.Write(header)
	if _, err = io.Copy(h, &contextReader{ctx, io.LimitReader(f, st.Size()-headerSize-sha256.Size)}); err != nil {
		return "", err
	}
	tail := make([]byte, sha256.Size)
	if _, err = io.ReadFull(f, tail); err != nil {
		return "", err
	}
	if !bytes.Equal(tail, h.Sum(nil)) {
		return "", ErrInvalid
	}
	return hex.EncodeToString(tail), nil
}

// Extract verifies before creating a NEW destination directory. It only restores
// regular files/directories, never links, devices, Git metadata or paths outside
// the directory. On failure it removes only the directory it just created.
func (s Store) Extract(ctx context.Context, ref, dest string) error {
	_, err := s.extract(ctx, ref, dest, false)
	return err
}

// Extraction retains verified archive metadata independently of the host's
// filesystem permissions. Modes contains slash-relative regular-file paths.
type Extraction struct {
	Metadata Metadata
	Modes    map[string]os.FileMode
}

// ExtractWithMetadata verifies the archive once and returns header and file-mode
// metadata from that same extraction. Like Extract, it creates a new destination.
func (s Store) ExtractWithMetadata(ctx context.Context, ref, dest string) (Extraction, error) {
	return s.extract(ctx, ref, dest, true)
}

func (s Store) extract(ctx context.Context, ref, dest string, trackModes bool) (result Extraction, err error) {
	key, sum, ok := strings.Cut(ref, ":")
	if !ok || !validHash(sum) {
		return result, ErrInvalid
	}
	f, err := s.open(key)
	if err != nil {
		return result, err
	}
	defer f.Close()
	got, err := s.check(ctx, f, key)
	if err != nil {
		return result, err
	}
	if got != sum {
		return result, ErrInvalid
	}
	st, err := f.Stat()
	if err != nil {
		return result, err
	}
	offset, meta, err := readMetadata(f)
	if err != nil {
		return result, err
	}
	if _, err = f.Seek(offset, io.SeekStart); err != nil {
		return result, err
	}
	if err = os.Mkdir(dest, 0700); err != nil {
		return result, err
	}
	defer func() {
		if err != nil {
			os.RemoveAll(dest)
		}
	}()
	result.Metadata = meta
	if trackModes {
		result.Modes = make(map[string]os.FileMode)
	}
	err = readTree(ctx, io.LimitReader(f, st.Size()-offset-sha256.Size), dest, result.Modes)
	if err != nil {
		return Extraction{}, err
	}
	return result, nil
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r *contextReader) Read(b []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(b)
}

type limitedWriter struct {
	w          io.Writer
	left       int64
	limitError error
}

func (w *limitedWriter) Write(b []byte) (int, error) {
	if int64(len(b)) > w.left {
		if w.limitError != nil {
			return 0, w.limitError
		}
		return 0, ErrTooLarge
	}
	n, err := w.w.Write(b)
	w.left -= int64(n)
	return n, err
}

func safePath(name string) bool {
	if name == "." || !fs.ValidPath(name) || strings.ContainsAny(name, "\\:") {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if strings.EqualFold(part, ".git") || strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") {
			return false
		}
	}
	return filepath.IsLocal(filepath.FromSlash(name))
}
func writeTree(ctx context.Context, out io.Writer, root string) error {
	st, err := os.Lstat(root)
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return ErrInvalid
	}
	tw := tar.NewWriter(out)
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if p == root {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if !safePath(rel) {
			return fmt.Errorf("%w: unsupported path", ErrInvalid)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() && !info.IsDir() {
			return fmt.Errorf("%w: only regular files and directories are supported", ErrInvalid)
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = rel
		header.Uid = 0
		header.Gid = 0
		header.Uname = ""
		header.Gname = ""
		if err = tw.WriteHeader(header); err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		opened, err := in.Stat()
		if err != nil || !os.SameFile(info, opened) {
			return ErrInvalid
		}
		n, err := io.Copy(tw, &contextReader{ctx, in})
		if err != nil {
			return err
		}
		if n != info.Size() {
			return ErrInvalid
		}
		return nil
	})
	if err != nil {
		return err
	}
	return tw.Close()
}
func readTree(ctx context.Context, in io.Reader, dest string, modes map[string]os.FileMode) error {
	tr := tar.NewReader(&contextReader{ctx, in})
	seen := map[string]bool{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if !safePath(h.Name) || seen[h.Name] {
			return ErrInvalid
		}
		seen[h.Name] = true
		if len(seen) > 1000000 {
			return ErrInvalid
		}
		path := filepath.Join(dest, filepath.FromSlash(h.Name))
		switch h.Typeflag {
		case tar.TypeDir:
			if err = os.MkdirAll(path, 0700); err != nil {
				return err
			}
		case tar.TypeReg:
			if modes != nil {
				modes[h.Name] = 0600 | os.FileMode(h.Mode)&0100
			}
			if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				return err
			}
			f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600|os.FileMode(h.Mode)&0100)
			if err != nil {
				return err
			}
			_, err = io.Copy(f, tr)
			closeErr := f.Close()
			if err != nil {
				return err
			}
			if closeErr != nil {
				return closeErr
			}
			if err = os.Chtimes(path, h.ModTime, h.ModTime); err != nil {
				return err
			}
		default:
			return ErrInvalid
		}
	}
}

// Metadata verifies the archive before returning its immutable seed reference.
func (s Store) Metadata(ctx context.Context, ref string) (Metadata, error) {
	key, sum, ok := strings.Cut(ref, ":")
	if !ok || !validHash(sum) {
		return Metadata{}, ErrInvalid
	}
	f, err := s.open(key)
	if err != nil {
		return Metadata{}, err
	}
	defer f.Close()
	actual, err := s.check(ctx, f, key)
	if err != nil {
		return Metadata{}, err
	}
	if actual != sum {
		return Metadata{}, ErrInvalid
	}
	_, meta, err := readMetadata(f)
	return meta, err
}
func readMetadata(f *os.File) (int64, Metadata, error) {
	var meta Metadata
	if _, err := f.Seek(headerSize, io.SeekStart); err != nil {
		return 0, meta, err
	}
	var length [4]byte
	if _, err := io.ReadFull(f, length[:]); err != nil {
		return 0, meta, err
	}
	n := binary.BigEndian.Uint32(length[:])
	if n > 64<<10 {
		return 0, meta, ErrInvalid
	}
	data := make([]byte, n)
	if _, err := io.ReadFull(f, data); err != nil {
		return 0, meta, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&meta); err != nil {
		return 0, meta, err
	}
	if len(meta.BaseReference) > 4096 || len(meta.SeedReference) > 4096 {
		return 0, meta, ErrInvalid
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return 0, meta, ErrInvalid
	}
	return headerSize + 4 + int64(n), meta, nil
}
