// Package backupgit protects the serialized bytes in a ClaudeRig backup from
// Git's text, encoding, keyword and clean/smudge conversions.
package backupgit

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const rule = "* -text -eol -filter -ident -working-tree-encoding"

// Ensure writes portable attributes into the backup itself, so they also apply
// during another machine's first clone. It preserves unrelated attribute rules.
func Ensure(root string) error {
	return EnsureContext(context.Background(), root)
}

// EnsureContext installs the same attributes with cancellation during reads and
// before replacement, for private queued publication workspaces.
func EnsureContext(ctx context.Context, root string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	p := filepath.Join(root, ".gitattributes")
	if st, err := os.Lstat(p); err == nil {
		if !st.Mode().IsRegular() {
			return fmt.Errorf("refusing non-regular backup .gitattributes")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	var b []byte
	in, err := os.Open(p)
	if err == nil {
		b, err = io.ReadAll(attributeReader{ctx, in})
		closeErr := in.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if strings.TrimSpace(lines[len(lines)-1]) == rule {
		return nil
	}
	if len(b) > 0 && b[len(b)-1] != '\n' {
		b = append(b, '\n')
	}
	b = append(b, []byte("# ClaudeRig backups must preserve their serialized bytes.\n"+rule+"\n")...)
	if err := ctx.Err(); err != nil {
		return err
	}
	f, err := os.CreateTemp(root, ".clauderig-attributes-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0o644); err == nil {
		_, err = f.Write(b)
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.Rename(f.Name(), p)
}

// Prepare upgrades an existing index as well as its working attributes. Git
// otherwise reuses cached normalized blobs for files whose stat data is unchanged.
// The caller must audit the working bytes before committing the resulting index.
func Prepare(ctx context.Context, root string) error {
	if err := Ensure(root); err != nil {
		return err
	}
	if err := Validate(ctx, root); err != nil {
		return err
	}
	current, err := os.ReadFile(filepath.Join(root, ".gitattributes"))
	if err != nil {
		return err
	}
	previous, err := git(ctx, root, nil, "show", "HEAD:.gitattributes")
	if err != nil || !bytes.Equal(previous, current) {
		if _, err := git(ctx, root, nil, "add", "--renormalize", "--", "."); err != nil {
			return err
		}
	}
	_, err = git(ctx, root, nil, "add", "--force", "--", ".gitattributes")
	return err
}

// Validate catches higher-priority info/attributes and nested .gitattributes
// that would transform bytes after the publication scan. It does not modify
// user Git configuration or invoke any filters.
func Validate(ctx context.Context, root string) error {
	var paths bytes.Buffer
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		paths.WriteString(filepath.ToSlash(rel))
		paths.WriteByte(0)
		return nil
	})
	if err != nil {
		return err
	}
	if paths.Len() == 0 {
		return nil
	}
	out, err := git(ctx, root, paths.Bytes(), "check-attr", "-z", "--stdin", "text", "eol", "filter", "ident", "working-tree-encoding")
	if err != nil {
		return err
	}
	fields := bytes.Split(bytes.TrimSuffix(out, []byte{0}), []byte{0})
	if len(fields)%3 != 0 {
		return fmt.Errorf("invalid Git attribute response")
	}
	for i := 0; i < len(fields); i += 3 {
		if string(fields[i+2]) != "unset" {
			return fmt.Errorf("backup Git attribute %s on %s permits byte conversion; remove the overriding attribute before publishing", fields[i+1], fields[i])
		}
	}
	return nil
}

func git(ctx context.Context, root string, input []byte, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir, cmd.Stdin = root, bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("backup git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

type attributeReader struct {
	ctx context.Context
	r   io.Reader
}

func (r attributeReader) Read(b []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(b)
}
