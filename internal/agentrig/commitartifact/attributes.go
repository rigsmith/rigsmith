package commitartifact

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/rigsmith/rigsmith/internal/agentrig/artifact"
)

var ErrAttributes = errors.New("publication attributes permit byte conversion")

// CheckUnsetAttributes evaluates Git's actual attribute syntax against a private
// raw publication tree. It uses an empty repository outside the tree, ignores
// inherited Git configuration and attributes, and never runs filters or writes
// the tree. Callers must own the tree throughout validation.
func CheckUnsetAttributes(ctx context.Context, root string, names []string) error {
	if !filepath.IsAbs(root) || len(names) == 0 || len(names) > 32 {
		return ErrInvalid
	}
	for _, name := range names {
		if len(name) == 0 || len(name) > 128 || strings.HasPrefix(name, "-") {
			return ErrInvalid
		}
		for _, ch := range name {
			if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '-' || ch == '_') {
				return ErrInvalid
			}
		}
	}
	var paths bytes.Buffer
	count := 0
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if p == root {
			if !d.IsDir() {
				return ErrInvalid
			}
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if !publicationPath(rel) {
			return ErrInvalid
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return ErrInvalid
		}
		if count >= 1000000 || paths.Len()+len(rel)+1 > 64<<20 {
			return artifact.ErrTooLarge
		}
		count++
		paths.WriteString(rel)
		paths.WriteByte(0)
		return nil
	})
	if err != nil {
		return err
	}
	if count == 0 {
		return ctx.Err()
	}
	work, err := os.MkdirTemp(filepath.Dir(root), ".attribute-check-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	repo, err := initRepo(ctx, filepath.Join(work, "git"), "")
	if err != nil {
		return err
	}
	args := append([]string{"--work-tree=" + root, "check-attr", "-z", "--stdin"}, names...)
	return repo.stream(ctx, bytes.NewReader(paths.Bytes()), func(output io.Reader) error {
		reader := bufio.NewReaderSize(publicationReader{ctx, output}, 8192)
		remaining := paths.Bytes()
		for len(remaining) > 0 {
			end := bytes.IndexByte(remaining, 0)
			path := string(remaining[:end])
			remaining = remaining[end+1:]
			for _, name := range names {
				for _, expected := range []string{path, name, "unset"} {
					field, err := reader.ReadSlice(0)
					if err != nil {
						return err
					}
					if string(field[:len(field)-1]) != expected {
						return ErrAttributes
					}
				}
			}
		}
		if _, err := reader.ReadByte(); err != io.EOF {
			if err != nil {
				return err
			}
			return ErrInvalid
		}
		return ctx.Err()
	}, args...)
}
