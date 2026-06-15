// Package copytree copies a repository's working tree to a fresh location,
// reusing walkutil's discovery rules so the copy skips the same noise every
// rigsmith tool ignores: the dependency trees and build output in
// walkutil.SkippedDir, plus the repo's root .gitignore'd paths. The VCS metadata
// directory (.git) is skipped by default — yielding a detached, history-free
// copy — and copied verbatim when IncludeGit is set, yielding a full independent
// repository.
//
// It is the engine behind `rig copy`. The destination is created fresh; callers
// are responsible for enforcing that it is absent or empty (Copy will happily
// merge into an existing tree), and Copy refuses only the one case it cannot do
// correctly: a destination nested inside the source.
package copytree

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/rigsmith/rigsmith/core/walkutil"
)

// Stats summarizes what a Copy moved, for the caller's completion line.
type Stats struct {
	Files       int   // regular files and symlinks copied (excludes .git internals)
	Dirs        int   // directories created (excludes .git internals)
	Bytes       int64 // total bytes of regular file content copied (excludes .git internals)
	GitIncluded bool  // whether a .git directory was copied verbatim
}

// Copy copies the tree rooted at src into dst. Directories in
// walkutil.SkippedDir (node_modules, vendor, build output, …) and paths matched
// by src's root .gitignore are skipped, exactly as walkutil.Walk would prune
// them. The .git directory is skipped unless includeGit is true, in which case
// it is copied verbatim (no skip set, no .gitignore filtering) so the
// destination is a complete, independent repository.
//
// dst is created if absent. Copy does not require dst to be empty — that policy
// belongs to the caller — but it does reject a dst nested inside src, which would
// otherwise copy the growing destination into itself.
func Copy(src, dst string, includeGit bool) (Stats, error) {
	var st Stats

	absSrc, err := filepath.Abs(src)
	if err != nil {
		return st, err
	}
	absDst, err := filepath.Abs(dst)
	if err != nil {
		return st, err
	}
	absSrc = filepath.Clean(absSrc)
	absDst = filepath.Clean(absDst)
	// Compare with symlinks resolved so a dst that lexically sits outside src but
	// actually resolves inside it (a symlinked dst, or a symlinked parent) can't
	// slip past the guard and make the copy write back into the source tree.
	realSrc := resolveExisting(absSrc)
	realDst := resolveExisting(absDst)
	if realDst == realSrc {
		return st, fmt.Errorf("destination is the same as the source: %s", absDst)
	}
	if strings.HasPrefix(realDst+string(os.PathSeparator), realSrc+string(os.PathSeparator)) {
		return st, fmt.Errorf("destination %s is inside the source %s", absDst, absSrc)
	}

	ign := walkutil.LoadIgnorer(absSrc)

	err = filepath.WalkDir(absSrc, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			// Skip unreadable subtrees rather than aborting the whole copy.
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		rel, err := filepath.Rel(absSrc, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return os.MkdirAll(absDst, 0o755)
		}
		relSlash := filepath.ToSlash(rel)
		target := filepath.Join(absDst, rel)

		// The repo's top-level .git gets dedicated handling. In a normal checkout
		// it's a directory copied verbatim under --git; in a linked worktree it's a
		// pointer FILE into the parent repo, which can't be copied meaningfully.
		if rel == ".git" {
			if !includeGit {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil // drop the worktree pointer file
			}
			if d.IsDir() {
				if err := copyVerbatim(p, target); err != nil {
					return err
				}
				st.GitIncluded = true
				return filepath.SkipDir
			}
			return fmt.Errorf("--git can't copy a linked worktree's git data (.git here is a pointer into the parent repo); run `rig copy --git` from the main checkout, or omit --git for a detached tree")
		}

		if d.IsDir() {
			if walkutil.SkippedDir(d.Name()) || ign.Ignored(relSlash, true) {
				return filepath.SkipDir
			}
			if err := os.MkdirAll(target, dirMode(d)); err != nil {
				return err
			}
			st.Dirs++
			return nil
		}

		if ign.Ignored(relSlash, false) {
			return nil
		}
		copied, n, err := copyEntry(p, target, d)
		if err != nil {
			return err
		}
		if copied {
			st.Files++
			st.Bytes += n
		}
		return nil
	})
	return st, err
}

// dirMode returns the directory's permission bits, defaulting to 0o755 when the
// entry's info can't be read.
func dirMode(d fs.DirEntry) fs.FileMode {
	info, err := d.Info()
	if err != nil {
		return 0o755
	}
	return info.Mode().Perm()
}

// copyEntry copies a single non-directory entry. Symlinks are recreated and
// regular files are copied (returning their byte count); irregular entries —
// named pipes, sockets, devices — are skipped, reported by copied=false, so the
// walk never blocks on os.Open of a fifo nor miscounts a non-file.
func copyEntry(src, dst string, d fs.DirEntry) (copied bool, n int64, err error) {
	if d.Type()&fs.ModeSymlink != 0 {
		return true, 0, copySymlink(src, dst)
	}
	if !d.Type().IsRegular() {
		return false, 0, nil
	}
	info, err := d.Info()
	if err != nil {
		return false, 0, err
	}
	n, err = copyFile(src, dst, info.Mode().Perm())
	return err == nil, n, err
}

// copyFile copies a regular file's contents, creating dst with the given mode.
func copyFile(src, dst string, mode fs.FileMode) (int64, error) {
	in, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(out, in)
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	return n, err
}

// copySymlink recreates a symlink at dst pointing at the same target src does.
func copySymlink(src, dst string) error {
	link, err := os.Readlink(src)
	if err != nil {
		return err
	}
	return os.Symlink(link, dst)
}

// copyVerbatim recursively copies srcDir to dstDir with no skip set or
// .gitignore filtering — used for the .git directory, whose internals must be
// reproduced exactly for the copy to be a usable repository.
func copyVerbatim(srcDir, dstDir string) error {
	return filepath.WalkDir(srcDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcDir, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dstDir, rel)
		if d.IsDir() {
			return os.MkdirAll(target, dirMode(d))
		}
		_, _, err = copyEntry(p, target, d)
		return err
	})
}

// resolveExisting returns path with symlinks resolved as far as the path exists,
// re-joining any not-yet-created trailing segments. It lets the dst-inside-src
// guard compare real locations even though dst itself usually doesn't exist yet.
func resolveExisting(path string) string {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return real
	}
	parent := filepath.Dir(path)
	if parent == path {
		return path // reached the root; nothing more to resolve
	}
	return filepath.Join(resolveExisting(parent), filepath.Base(path))
}
