// Package backupgit protects the serialized bytes in a rig's backup from Git's
// text, encoding, keyword and clean/smudge conversions.
//
// It is shared by every rig that commits an agent's files, and sharing it is not
// a tidiness argument. This is the only thing standing between a machine with an
// aggressive global .gitattributes — `* text=auto eol=crlf`, a clean/smudge
// filter, an encoding rule — and a backup whose bytes no longer match what was
// scanned. The failure is silent on the machine that publishes and only shows up
// on the one that restores, so a second copy of these rules that fell behind
// would be a second way to corrupt somebody's data.
package backupgit

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const rule = "* -text -eol -filter -ident -working-tree-encoding"

// Ensure writes portable attributes into the backup itself, so they also apply
// during another machine's first clone — before any local config can be
// consulted, which is exactly when a hostile global rule would otherwise win.
// It preserves unrelated attribute rules.
//
// tool names the rig in the comment line. Not cosmetic: this file is written
// into the user's own backup repository, and a codexrig backup explaining
// itself in clauderig's name is the same class of mistake as the fallback
// commit identity that said "clauderig" for both. Keeping clauderig's existing
// wording byte for byte also means an existing backup is not rewritten, and the
// compatibility baseline stays green for the tool that has shipped.
func Ensure(root, tool string) error {
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
	b, err := os.ReadFile(p)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if strings.TrimSpace(lines[len(lines)-1]) == rule {
		return nil
	}
	if len(b) > 0 && b[len(b)-1] != '\n' {
		b = append(b, '\n')
	}
	b = append(b, []byte("# "+tool+" backups must preserve their serialized bytes.\n"+rule+"\n")...)
	f, err := os.CreateTemp(root, ".rigsmith-attributes-*")
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
	return os.Rename(f.Name(), p)
}

// Prepare upgrades an existing index as well as its working attributes. Git
// otherwise reuses cached normalized blobs for files whose stat data is unchanged.
// The caller must audit the working bytes before committing the resulting index.
func Prepare(ctx context.Context, root, tool string) error {
	if err := Ensure(root, tool); err != nil {
		return err
	}
	if err := dropDeletedAttributes(ctx, root); err != nil {
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

// dropDeletedAttributes forgets index entries for .gitattributes files the
// working tree no longer has. check-attr falls back to the index for a file
// that is gone from disk, so one the allowlist has just stopped syncing goes on
// governing the backup after it is deleted — invisible, and refusing every
// publish, with nothing left to delete.
//
// Removals only, and only of these files: a refused Prepare must still add
// nothing to the index.
func dropDeletedAttributes(ctx context.Context, root string) error {
	out, err := git(ctx, root, nil, "ls-files", "--deleted", "-z", "--", ":(glob)**/.gitattributes")
	if err != nil {
		return err
	}
	out = bytes.TrimRight(out, "\x00")
	if len(out) == 0 {
		return nil
	}
	args := []string{"rm", "--cached", "--quiet", "--"}
	for _, p := range bytes.Split(out, []byte{0}) {
		args = append(args, string(p))
	}
	_, err = git(ctx, root, nil, args...)
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
