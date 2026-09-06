// Package files implements vendor-independent snapshot and restore mechanics.
// Callers choose paths, permissions, codecs, and retention policy.
package files

import (
	"io"
	"os"
	"path/filepath"
	"time"
)

// Permissions selects creation modes; existing files retain their modes.
type Permissions struct{ Dir, File os.FileMode }

// DirExists reports whether p resolves to a directory.
func DirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

// Write creates parent directories and writes bytes using caller-selected modes.
func Write(path string, data []byte, pm Permissions) error {
	if err := os.MkdirAll(filepath.Dir(path), pm.Dir); err != nil {
		return err
	}
	return os.WriteFile(path, data, pm.File)
}

// RemoveEmptyDirs removes empty descendants, deepest first, retaining root.
// Cleanup is best-effort, including for unreadable subtrees.
func RemoveEmptyDirs(root string) {
	var dirs []string
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			dirs = append(dirs, p)
		}
		return nil
	})
	for i := len(dirs) - 1; i >= 0; i-- {
		if dirs[i] != root {
			_ = os.Remove(dirs[i]) // removes only if empty
		}
	}
}

// WriteMtime stages bytes with 0755 directories and 0644 files, preserving mtime.
// This is a direct write for bytes already scanned by the caller.
func WriteMtime(dst string, data []byte, mtime time.Time) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		return err
	}
	return os.Chtimes(dst, mtime, mtime)
}

// CopySnapshot streams into a temporary sibling and publishes by rename only
// after copying and setting mtime succeed. The published file retains the
// temporary file's 0600 creation mode; parent directories use 0755.
func CopySnapshot(src, dst string, mtime time.Time) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	// Written beside dst and renamed over it, so an interrupted copy leaves
	// the previous staged file where it was rather than a truncated one in
	// its place: the large-file throttle compares against the staged copy,
	// and a truncated copy would pass for a baseline and then be committed.
	out, err := os.CreateTemp(filepath.Dir(dst), "."+filepath.Base(dst)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := out.Name()
	fail := func(err error) error {
		_ = out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		return fail(err)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Chtimes(tmp, mtime, mtime); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// Reconcile removes staged files rejected by allowed (slash-relative paths).
// Allowed files survive even when absent on this machine. Missing entries and
// individual removal failures are best-effort; other walk errors are returned.
func Reconcile(stageRoot string, allowed func(string) bool) (removed int, err error) {
	if !DirExists(stageRoot) {
		return 0, nil
	}
	err = filepath.WalkDir(stageRoot, func(p string, d os.DirEntry, werr error) error {
		if werr != nil {
			// A staged tree churning under us is not a reason to fail the sync.
			if os.IsNotExist(werr) {
				return nil
			}
			return werr
		}
		if d.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(stageRoot, p)
		if rerr != nil {
			return nil
		}
		if !allowed(filepath.ToSlash(rel)) {
			if os.Remove(p) == nil {
				removed++
			}
			return nil
		}
		return nil
	})
	if err != nil {
		return removed, err
	}
	RemoveEmptyDirs(stageRoot)
	return removed, nil
}

// Copy streams bytes directly with caller-selected creation modes. Unlike
// CopySnapshot, it truncates an existing destination and does not preserve mtime.
func Copy(src, dst string, pm Permissions) error {
	if err := os.MkdirAll(filepath.Dir(dst), pm.Dir); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, pm.File)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
