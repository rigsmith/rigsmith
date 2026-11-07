package cli

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

func stackReleaseExcludedDir(name string) bool {
	switch strings.ToLower(name) {
	case ".git", "bin", "obj", ".vs", "node_modules":
		return true
	}
	return false
}

// All release readers and editors share this scope, including C# guard files.
// Git decides which untracked files are output; tracked files always count.
// Seed snapshots have no worktree, so they use the same filters over a walk.
func stackReleaseWalkFiles(root string, visit func(string) error) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	info, err := os.Lstat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("release root is not a regular directory: %s", root)
	}
	ctx := context.Background()
	_, gitErr := stackCIGit(ctx, root, "rev-parse", "--show-toplevel")
	var paths []string
	if gitErr == nil {
		listed, err := stackCIGit(ctx, root, "--literal-pathspecs", "ls-files", "-z", "--cached", "--others", "--exclude-standard", "--", ".")
		if err != nil {
			return err
		}
		for _, rel := range strings.Split(string(listed), "\x00") {
			if rel != "" {
				paths = append(paths, filepath.Join(root, filepath.FromSlash(rel)))
			}
		}
	} else {
		if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if path != root && stackReleaseExcludedDir(entry.Name()) {
					return filepath.SkipDir
				}
			} else {
				paths = append(paths, path)
			}
			return nil
		}); err != nil {
			return err
		}
	}
	slices.Sort(paths)
	for _, path := range slices.Compact(paths) {
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("release candidate outside root: %s", path)
		}
		dirs := strings.Split(filepath.ToSlash(filepath.Dir(rel)), "/")
		if slices.ContainsFunc(dirs, stackReleaseExcludedDir) {
			continue
		}
		switch strings.ToLower(filepath.Ext(path)) {
		case ".cs", ".csproj", ".props", ".targets":
		default:
			continue
		}
		// Check ancestors too: an indexed path can pass through a directory
		// replaced by a symlink since the index was written.
		for candidate := path; candidate != root; candidate = filepath.Dir(candidate) {
			info, err := os.Lstat(candidate)
			if err != nil {
				return err
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("release candidate is a symlink: %s", candidate)
			}
		}
		if err := visit(path); err != nil {
			return err
		}
	}
	return nil
}
