package files

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

var (
	ErrSource        = errors.New("source must be a regular file in an unchanged directory")
	ErrSourceChanged = errors.New("source changed during capture")
	ErrSourceLimit   = errors.New("source capture limit exceeded")
)

// Source pins a directory handle for bounded, read-only capture. Its named root
// must remain the same non-symlink directory. Ancestor aliases are allowed.
// Callers must close it and choose which direct-child names may be read. Checks
// detect observed changes; they are not a transaction with uncooperative writers.
// Source methods are intended for sequential use.
type Source struct {
	root *os.Root
	path string
	info os.FileInfo
}

func OpenSource(ctx context.Context, path string) (*Source, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" && runtime.GOOS != "windows" {
		return nil, ErrSource
	}
	if !filepath.IsAbs(path) {
		return nil, ErrSource
	}
	path = filepath.Clean(path)
	info, err := os.Lstat(path)
	if err != nil {
		return nil, sourceError(err)
	}
	if !info.IsDir() {
		return nil, ErrSource
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, sourceError(err)
	}
	s := &Source{root: root, path: path, info: info}
	if err := s.Check(ctx); err != nil {
		root.Close()
		return nil, err
	}
	return s, nil
}

func (s *Source) Close() error { return s.root.Close() }

// Check verifies both the pinned handle and its current directory name.
func (s *Source) Check(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	current, err := os.Lstat(s.path)
	if err != nil {
		return sourceError(err)
	}
	pinned, err := s.root.Stat(".")
	if err != nil {
		return sourceError(err)
	}
	if !current.IsDir() || !os.SameFile(s.info, current) || !os.SameFile(s.info, pinned) {
		return ErrSourceChanged
	}
	return nil
}

// Names lists direct children only, capped before collecting the whole directory.
// It never traverses or opens the children. Returned names are sorted.
func (s *Source) Names(ctx context.Context, limit int) ([]string, error) {
	if limit <= 0 {
		return nil, ErrSourceLimit
	}
	if err := s.Check(ctx); err != nil {
		return nil, err
	}
	dir, err := s.root.Open(".")
	if err != nil {
		return nil, sourceError(err)
	}
	defer dir.Close()
	var names []string
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		batchSize := 128
		if remaining := limit - len(names); remaining < batchSize {
			batchSize = remaining + 1
		}
		batch, err := dir.Readdirnames(batchSize)
		names = append(names, batch...)
		if len(names) > limit {
			return nil, ErrSourceLimit
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, sourceError(err)
		}
	}
	if err := s.Check(ctx); err != nil {
		return nil, err
	}
	sort.Strings(names)
	return names, nil
}

// Read reads a regular direct child through the pinned directory. It rejects
// links and special files before reading, rechecks identity/metadata afterward,
// and checks cancellation between 32 KiB blocks. OS syscalls can outlast context
// cancellation. No bytes are returned on errors, including a changed source.
func (s *Source) Read(ctx context.Context, name string, limit int64) ([]byte, error) {
	if limit <= 0 {
		return nil, ErrSourceLimit
	}
	if !filepath.IsLocal(name) || name == "." || strings.ContainsAny(name, "/\\:") {
		return nil, ErrSource
	}
	if err := s.Check(ctx); err != nil {
		return nil, err
	}
	before, err := s.root.Lstat(name)
	if err != nil {
		return nil, sourceError(err)
	}
	if !before.Mode().IsRegular() {
		return nil, ErrSource
	}
	if before.Size() > limit {
		return nil, ErrSourceLimit
	}
	f, err := openSourceFile(s.root, name)
	if err != nil {
		return nil, sourceError(err)
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return nil, sourceError(err)
	}
	named, err := s.root.Lstat(name)
	if err != nil {
		return nil, sourceError(err)
	}
	if !sameSource(before, opened) || !sameSource(before, named) {
		return nil, ErrSourceChanged
	}
	var out bytes.Buffer
	var block [32 << 10]byte
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, err := f.Read(block[:])
		if int64(out.Len())+int64(n) > limit {
			return nil, ErrSourceLimit
		}
		out.Write(block[:n])
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, sourceError(err)
		}
		if n == 0 {
			return nil, io.ErrNoProgress
		}
	}
	after, err := f.Stat()
	if err != nil {
		return nil, sourceError(err)
	}
	named, err = s.root.Lstat(name)
	if err != nil {
		return nil, sourceError(err)
	}
	if !sameSource(before, after) || !sameSource(before, named) || int64(out.Len()) != before.Size() {
		return nil, ErrSourceChanged
	}
	if err := s.Check(ctx); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func sameSource(a, b os.FileInfo) bool {
	return b.Mode().IsRegular() && os.SameFile(a, b) && a.Size() == b.Size() && a.Mode() == b.Mode() && a.ModTime().Equal(b.ModTime())
}

// Strip filesystem path/error text: filenames and OS messages can contain
// private data. Preserve missing and permission categories for callers.
func sourceError(err error) error {
	switch {
	case errors.Is(err, os.ErrNotExist):
		return os.ErrNotExist
	case errors.Is(err, os.ErrPermission):
		return os.ErrPermission
	default:
		return ErrSource
	}
}
