package hooks

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func canonicalHookPath(path string) (string, error) {
	if runtime.GOOS != "windows" && strings.Contains(path, `\`) {
		if _, err := os.Lstat(path); err == nil {
			return "", fmt.Errorf("literal backslash in hook path")
		}
		path = strings.ReplaceAll(path, `\`, "/")
	}
	path = filepath.FromSlash(path)
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("hook path is not absolute")
	}
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if part == ".." {
			return "", fmt.Errorf("parent traversal in hook path")
		}
	}
	var suffix []string
	for {
		resolved, err := filepath.EvalSymlinks(path)
		if err == nil {
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			if runtime.GOOS == "windows" {
				resolved = strings.ToLower(resolved)
			}
			return resolved, nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		// A dangling symlink is not an absent directory we can create safely.
		if st, lerr := os.Lstat(path); lerr == nil && st.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("dangling hook symlink")
		}
		parent := filepath.Dir(path)
		if parent == path {
			return "", err
		}
		suffix = append(suffix, filepath.Base(path))
		path = parent
	}
}

// ValidateProject resolves existing ancestors before allowing any project write.
func ValidateProject(repo, path string) error {
	fail := func(reason string) error {
		return fmt.Errorf("refusing project write %s: %s (check symlinks)", Display(path), Display(reason))
	}
	root, err := canonicalHookPath(repo)
	if err != nil {
		return fail(err.Error())
	}
	target, err := canonicalHookPath(path)
	if err != nil {
		return fail(err.Error())
	}
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fail("resolves outside repository")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	for _, name := range []string{"settings.json", "settings.local.json"} {
		global, e := canonicalHookPath(filepath.Join(home, ".claude", name))
		if e == nil && global == target {
			return fail("resolves to global settings")
		}
	}
	return nil
}
