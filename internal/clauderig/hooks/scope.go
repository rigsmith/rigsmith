package hooks

import (
	"fmt"
	"os"
	"os/exec"
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
	if err := rejectGitMetadata(root, target, rel); err != nil {
		return fail(err.Error())
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

// The .git entry itself is protected even when it is a linked-worktree gitfile.
// Git can also place its private and common directories elsewhere, including
// beneath another name inside the checkout.
func rejectGitMetadata(root, target, rel string) error {
	if first := strings.SplitN(filepath.ToSlash(rel), "/", 2)[0]; strings.EqualFold(first, ".git") {
		return fmt.Errorf("resolves into git metadata")
	}
	if _, err := os.Lstat(filepath.Join(root, ".git")); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	for _, flag := range []string{"--git-dir", "--git-common-dir"} {
		cmd := exec.Command("git", "-C", root, "rev-parse", "--path-format=absolute", flag)
		// Bind discovery to root/.git, not a repository selected by the caller.
		cmd.Env = make([]string, 0, len(os.Environ()))
		for _, entry := range os.Environ() {
			key, _, _ := strings.Cut(entry, "=")
			switch strings.ToUpper(key) {
			case "GIT_DIR", "GIT_COMMON_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_CEILING_DIRECTORIES":
				continue
			}
			cmd.Env = append(cmd.Env, entry)
		}
		raw, err := cmd.Output()
		if err != nil {
			return fmt.Errorf("cannot resolve git metadata: %w", err)
		}
		dir, err := canonicalHookPath(strings.TrimSuffix(strings.TrimSuffix(string(raw), "\n"), "\r"))
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(dir, target)
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative) {
			return fmt.Errorf("resolves into git metadata")
		}
	}
	return nil
}
