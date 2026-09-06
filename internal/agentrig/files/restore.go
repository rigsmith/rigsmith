package files

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// RestoreOptions supplies policy for one staged tree. Paths passed to Plan and
// recorded in results are slash-separated and relative to the respective root.
// Plan runs before Live is checked. Write is required and owns byte decoding.
// Roots must be trusted; guards protect existing local state, not concurrent
// hostile filesystem mutation. Restore never prunes on its own.
type RestoreOptions struct {
	SourceDir, TargetDir string
	Plan                 func(sourceRel string) (targetRel string, skip bool)
	Live                 map[string]bool
	Write                func(src, dst, sourceRel, targetRel string) error
}

// RestoreResult records accepted files and destinations kept for local state.
// Written includes symlink and conflict skips for compatibility with pruning.
// Protected preserves the entire subtree of a refused directory destination.
type RestoreResult struct {
	Files, LinksKept, Conflicts int
	LiveSkipped                 []string
	Written, Protected          map[string]bool
}

// Restore applies the caller's codec only after live, symlink, and collision
// checks. Failure stops the loop; callers must not prune after an error.
func Restore(opts RestoreOptions) (*RestoreResult, error) {
	if opts.Write == nil {
		return nil, fmt.Errorf("restore: missing write callback")
	}
	paths, err := listFiles(opts.SourceDir)
	if err != nil {
		return nil, err
	}
	result := &RestoreResult{Written: map[string]bool{}, Protected: map[string]bool{}}
	links := LinkCache{}
	for _, rel := range paths {
		targetRel := rel
		if opts.Plan != nil {
			var skip bool
			targetRel, skip = opts.Plan(rel)
			if skip {
				continue
			}
		}
		if !filepath.IsLocal(filepath.FromSlash(targetRel)) || targetRel == "." {
			return nil, fmt.Errorf("restore: invalid relative destination %q", targetRel)
		}
		if opts.Live[targetRel] {
			result.LiveSkipped = append(result.LiveSkipped, targetRel)
			continue
		}
		src := filepath.Join(opts.SourceDir, filepath.FromSlash(rel))
		dst := filepath.Join(opts.TargetDir, filepath.FromSlash(targetRel))
		if isSymlink(dst) || links.UnderSymlink(opts.TargetDir, dst) {
			result.Written[targetRel] = true
			result.LinksKept++
			continue
		}
		if conflictAt(opts.TargetDir, dst) {
			result.Written[targetRel] = true
			result.Protected[targetRel] = true
			result.Conflicts++
			continue
		}
		if err := opts.Write(src, dst, rel, targetRel); err != nil {
			return nil, err
		}
		result.Written[targetRel] = true
		result.Files++
	}
	return result, nil
}

// Prune removes absent files only under explicitly authoritative directories.
// It preserves symlinks and protected subtrees, and leaves empty directories.
// dirs must be trusted root-relative directory paths selected by the caller.
func Prune(target string, dirs []string, written, protected map[string]bool) (int, error) {
	pruned := 0
	for _, dir := range dirs {
		base := filepath.Join(target, dir)
		if !DirExists(base) {
			continue
		}
		err := filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			// Never delete a symlink. It is the machine's own state — the same
			// rule the restore loop applies when it declines to write through
			// one — and a link is recorded in `written` only under the
			// DESCENDANT path that was skipped, so judging the link itself by
			// that map would collect it every time.
			if d.Type()&fs.ModeSymlink != 0 {
				return nil
			}
			rel, rerr := filepath.Rel(target, p)
			if rerr != nil {
				return rerr
			}
			relSlash := filepath.ToSlash(rel)
			if underProtected(relSlash, protected) {
				return nil
			}
			if !written[relSlash] {
				if err := os.Remove(p); err != nil {
					return err
				}
				pruned++
			}
			return nil
		})
		if err != nil {
			return pruned, err
		}
	}
	return pruned, nil
}

func listFiles(root string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return rerr
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	return out, err
}

// LinkCache remembers which destination directories sit on or under a symlink,
// so the ancestor walk costs one Lstat per directory across the whole restore
// rather than one per path component per file.
type LinkCache map[string]bool

// UnderSymlink reports whether any ancestor of dst, up to and excluding root, is
// a symlink. root itself is never judged: the target directory is where the user
// pointed restore, and following it is the whole intent.
func (c LinkCache) UnderSymlink(root, dst string) bool {
	dir := filepath.Dir(dst)
	if v, ok := c[dir]; ok {
		return v
	}
	rel, err := filepath.Rel(root, dir)
	// Only ".." itself, or a path BELOW it, is outside the root. A bare prefix
	// test would also catch a real directory named "..memory" — Rel returns that
	// name unchanged — and skip the very symlink check this exists to perform.
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false // at or outside the root — nothing left to walk
	}
	res := isSymlink(dir) || c.UnderSymlink(root, dir)
	c[dir] = res
	return res
}

// conflictAt reports whether something at or above dst makes it unwriteable:
// a directory occupying dst itself, or a regular FILE occupying one of its
// ancestors.
//
// Both end the same way if written through — EISDIR from the open, or ENOTDIR
// from MkdirAll — and both would take the whole restore down over one path. The
// ancestor case is easy to miss because Lstat(dst) returns ENOTDIR rather than
// describing dst, so a check that only inspects dst never sees it.
func conflictAt(root, dst string) bool {
	if fi, err := os.Lstat(dst); err == nil && fi.IsDir() {
		return true
	}
	for dir := filepath.Dir(dst); ; dir = filepath.Dir(dir) {
		rel, err := filepath.Rel(root, dir)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return false
		}
		if fi, lerr := os.Lstat(dir); lerr == nil && !fi.IsDir() {
			return true // a file where a directory has to be
		}
	}
}

// underProtected reports whether rel sits at or beneath a destination restore
// declined to write.
func underProtected(rel string, protected map[string]bool) bool {
	for p := range protected {
		if rel == p || strings.HasPrefix(rel, p+"/") {
			return true
		}
	}
	return false
}

// isSymlink reports whether p is a symlink, without following it. A missing path
// is not a symlink, so a fresh machine takes the ordinary write path.
func isSymlink(p string) bool {
	fi, err := os.Lstat(p)
	return err == nil && fi.Mode()&fs.ModeSymlink != 0
}
