package hooks

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/rigsmith/rigsmith/internal/codexrig/codexhome"
	"github.com/rigsmith/rigsmith/internal/codexrig/guard"
)

// Scope binds both the file and the plans the corresponding installer writes.
type Scope struct {
	path, name, repo string
	globalFiles      []string
}

func GlobalScope(home string) Scope {
	if home == "" {
		return Scope{}
	}
	return Scope{path: filepath.Join(home, FileName), name: "global"}
}

func ProjectScope(repo string, homes ...string) Scope {
	if repo == "" {
		return Scope{}
	}
	if home, err := codexhome.Default(); err == nil {
		homes = append(homes, home)
	}
	if home, ok := codexhome.Env(); ok {
		homes = append(homes, home)
	}
	s := Scope{path: filepath.Join(repo, ".codex", FileName), name: "project", repo: repo}
	for _, home := range homes {
		if home != "" {
			s.globalFiles = append(s.globalFiles, filepath.Join(home, FileName))
		}
	}
	return s
}

func (s Scope) Plans() []Plan {
	if s.name == "global" {
		return SyncPlans()
	}
	if s.name == "project" {
		return GuardPlans(guard.Matcher())
	}
	return nil
}

// canonicalHookPath refuses ambiguous literal backslashes and parent traversal.
// Resolve existing components before appending absent installation directories.
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

func (s Scope) Validate() error {
	if s.path == "" {
		return fmt.Errorf("explicit hook scope required")
	}
	path, err := canonicalHookPath(s.path)
	if err != nil {
		return err
	}
	if s.name != "project" {
		return nil
	}
	root, err := canonicalHookPath(s.repo)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("project hooks resolve outside the repository; remove the escaping symlink before installing or trusting")
	}
	for _, global := range s.globalFiles {
		resolved, err := canonicalHookPath(global)
		if err == nil && path == resolved {
			return fmt.Errorf("project hooks resolve to the global hooks file; remove the symlink before installing or trusting")
		}
	}
	return nil
}

func (s Scope) addresses(source string) bool {
	a, err := canonicalHookPath(s.path)
	if err != nil {
		return false
	}
	b, err := canonicalHookPath(source)
	return err == nil && a == b
}

func (s Scope) Contains(source string) bool { return s.Validate() == nil && s.addresses(source) }

// References is for diagnostics of unsafe scopes too; never authorize with it.
func (s Scope) References(source string) bool { return s.addresses(source) }

// InstallScope refuses project symlinks before any write takes place.
func InstallScope(scope Scope) (added, updated []string, err error) {
	if err := scope.Validate(); err != nil {
		return nil, nil, DisplayError(err)
	}
	added, updated, err = Install(scope.path, scope.Plans())
	return added, updated, DisplayError(err)
}
