package hooks

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestHardeningScope(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, file := range []string{"settings.json", "settings.local.json"} {
		for _, linkDir := range []bool{false, true} {
			root := t.TempDir()
			outside := filepath.Join(home, ".claude")
			if err := os.MkdirAll(outside, 0755); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(outside, file)
			if err := os.WriteFile(target, []byte("{}"), 0644); err != nil {
				t.Fatal(err)
			}
			if linkDir {
				if err := os.Symlink(outside, filepath.Join(root, ".claude")); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			} else {
				if err := os.MkdirAll(filepath.Join(root, ".claude"), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, filepath.Join(root, ".claude", file)); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			}
			if err := ValidateProject(root, filepath.Join(root, ".claude", file)); err == nil {
				t.Fatal("accepted escaping symlink")
			}
		}
	}
	root := t.TempDir()
	alias := filepath.Join(t.TempDir(), "repo")
	if err := os.Symlink(root, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := ValidateProject(alias, filepath.Join(alias, ".claude", "settings.json")); err != nil {
		t.Fatal(err)
	}
}
func TestHardeningOwnership(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path := settingsPath(t, `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo clauderig"},{"type":"command","command":"clauderig sync"},{"type":"command","command":"keep"}]}]}}`)
	if _, err := Install(path, SyncPlans()); err != nil {
		t.Fatal(err)
	}
	if _, err := Uninstall(path); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "echo clauderig") || !strings.Contains(string(raw), "keep") || strings.Contains(string(raw), "clauderig sync") {
		t.Fatalf("foreign sibling lost or stale retained: %s", raw)
	}
}
func TestHardeningShapes(t *testing.T) {
	for _, raw := range []string{`null`, `{"hooks":[]}`, `{"hooks":{"Stop":{}}}`, `{"hooks":{"Stop":[null]}}`, `{"hooks":{"Stop":[{"hooks":null}]}}`} {
		p := settingsPath(t, raw)
		if _, err := Install(p, SyncPlans()); err == nil {
			t.Fatalf("accepted %s", raw)
		}
		after, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if string(after) != raw {
			t.Fatal("modified invalid file")
		}
	}
	p := settingsPath(t, `{"large":9007199254740993}`)
	if _, err := Install(p, SyncPlans()); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "9007199254740993") {
		t.Fatal(string(b))
	}
}
func TestHardeningDisplay(t *testing.T) {
	got := Display("a\x1b\n\u202eb")
	if strings.ContainsAny(got, "\x1b\n\u202e") {
		t.Fatal(got)
	}
}

func TestHardeningGitMetadata(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	git := func(t *testing.T, args ...string) string {
		t.Helper()
		out, err := exec.Command("git", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	link := func(t *testing.T, target, path string) {
		t.Helper()
		if err := os.Symlink(target, path); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
	}
	refuse := func(t *testing.T, root, path string) {
		t.Helper()
		if err := ValidateProject(root, path); err == nil || !strings.Contains(err.Error(), "git metadata") {
			t.Fatalf("expected metadata refusal for %s, got %v", path, err)
		}
	}
	t.Run("directory", func(t *testing.T) {
		root := t.TempDir()
		git(t, "init", root)
		target := filepath.Join(root, ".git", "hooks", "pre-commit")
		if err := os.WriteFile(target, []byte("original"), 0644); err != nil {
			t.Fatal(err)
		}
		guide := filepath.Join(root, "CLAUDE.md")
		link(t, target, guide)
		refuse(t, root, guide)
		if err := os.MkdirAll(filepath.Join(root, ".claude"), 0755); err != nil {
			t.Fatal(err)
		}
		settings := filepath.Join(root, ".claude", "settings.json")
		link(t, filepath.Join("..", ".git", "config"), settings)
		refuse(t, root, settings)
		// A similarly named ordinary directory is not metadata.
		if err := ValidateProject(root, filepath.Join(root, ".github", "settings.json")); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("separate-directory", func(t *testing.T) {
		root := t.TempDir()
		metadata := filepath.Join(root, "private-metadata")
		git(t, "init", "--separate-git-dir", metadata, root)
		guide := filepath.Join(root, "CLAUDE.md")
		link(t, filepath.Join(metadata, "config"), guide)
		refuse(t, root, guide)
	})
	t.Run("nested-repository", func(t *testing.T) {
		root := t.TempDir()
		git(t, "init", root)
		nested := filepath.Join(root, "nested")
		git(t, "init", nested)
		guide := filepath.Join(root, "CLAUDE.md")
		link(t, filepath.Join(nested, ".git", "config"), guide)
		refuse(t, root, guide)
	})
	t.Run("case-variant", func(t *testing.T) {
		root := t.TempDir()
		metadata := filepath.Join(root, "private-metadata")
		git(t, "init", "--separate-git-dir", metadata, root)
		variant := filepath.Join(root, "PRIVATE-METADATA")
		info, err := os.Stat(metadata)
		if err != nil {
			t.Fatal(err)
		}
		other, err := os.Stat(variant)
		if os.IsNotExist(err) {
			t.Skip("case-sensitive filesystem")
		}
		if err != nil {
			t.Fatal(err)
		}
		if !os.SameFile(info, other) {
			t.Skip("case variants name different directories")
		}
		guide := filepath.Join(root, "CLAUDE.md")
		link(t, filepath.Join(variant, "config"), guide)
		refuse(t, root, guide)
		// Also exercise the .git spelling requested by the review.
		nested := filepath.Join(root, "nested")
		git(t, "init", nested)
		path := filepath.Join(root, "guide.md")
		link(t, filepath.Join(nested, ".GIT", "config"), path)
		refuse(t, root, path)
	})
	for _, name := range []string{"metadata\\literal", "metadata\r/repo"} {
		t.Run(name, func(t *testing.T) {
			if runtime.GOOS == "windows" {
				t.Skip("Unix filename bytes")
			}
			root := t.TempDir()
			metadata := filepath.Join(root, name)
			if err := os.MkdirAll(filepath.Dir(metadata), 0755); err != nil {
				t.Fatal(err)
			}
			git(t, "init", "--separate-git-dir", metadata, root)
			guide := filepath.Join(root, "CLAUDE.md")
			link(t, filepath.Join(metadata, "config"), guide)
			refuse(t, root, guide)
			if err := ValidateProject(root, filepath.Join(root, ".claude", "settings.json")); err != nil {
				t.Fatal(err)
			}
		})
	}
	t.Run("broken-gitfile-fails-closed", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: missing-metadata\n"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := ValidateProject(root, filepath.Join(root, ".claude", "settings.json")); err == nil {
			t.Fatal("accepted failed metadata discovery")
		}
	})
	t.Run("hostile-git-environment", func(t *testing.T) {
		root := t.TempDir()
		metadata := filepath.Join(root, "private-metadata")
		git(t, "init", "--separate-git-dir", metadata, root)
		other := t.TempDir()
		git(t, "init", other)
		guide := filepath.Join(root, "CLAUDE.md")
		link(t, filepath.Join(metadata, "config"), guide)
		overrides := map[string]string{
			"GIT_DIR":                 filepath.Join(other, ".git"),
			"GIT_COMMON_DIR":          filepath.Join(other, ".git"),
			"GIT_WORK_TREE":           other,
			"GIT_INDEX_FILE":          filepath.Join(other, ".git", "index"),
			"GIT_CEILING_DIRECTORIES": root,
		}
		for key, value := range overrides {
			t.Run(key, func(t *testing.T) {
				t.Setenv(key, value)
				refuse(t, root, guide)
				if err := ValidateProject(root, filepath.Join(root, ".claude", "settings.json")); err != nil {
					t.Fatalf("ordinary project path rejected under %s: %v", key, err)
				}
			})
		}
	})
	t.Run("linked-worktree", func(t *testing.T) {
		base := t.TempDir()
		git(t, "init", base)
		git(t, "-C", base, "-c", "user.name=Example", "-c", "user.email=you@example.com", "commit", "--allow-empty", "-m", "initial")
		root := filepath.Join(t.TempDir(), "checkout")
		git(t, "-C", base, "worktree", "add", "--detach", root)
		t.Cleanup(func() { git(t, "-C", base, "worktree", "remove", "--force", root) })
		guide := filepath.Join(root, "CLAUDE.md")
		link(t, filepath.Join(root, ".git"), guide)
		refuse(t, root, guide)
		for _, flag := range []string{"--git-dir", "--git-common-dir"} {
			metadata := git(t, "-C", root, "rev-parse", "--path-format=absolute", flag)
			if err := rejectGitMetadata(root, metadata); err == nil {
				t.Fatalf("accepted actual %s", flag)
			}
		}
	})
}

func TestHardeningGitMetadataPath(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{".git", filepath.Join("private-metadata", "repo"), filepath.Join(root, "absolute-metadata")} {
		want := path
		if !filepath.IsAbs(want) {
			want = filepath.Join(root, want)
		}
		if got := gitMetadataPath(root, []byte(path+"\n")); got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	}
	if runtime.GOOS == "windows" {
		if got := gitMetadataPath(root, []byte(".git\r\n")); got != filepath.Join(root, ".git") {
			t.Fatal(got)
		}
	} else {
		if got := gitMetadataPath(root, []byte("metadata\r\n")); got != filepath.Join(root, "metadata\r") {
			t.Fatal(got)
		}
	}
}
