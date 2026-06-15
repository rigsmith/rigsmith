package copytree

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// tree returns every regular file and directory under root as sorted,
// root-relative '/' paths, with a trailing '/' marking directories.
func tree(t *testing.T, root string) []string {
	t.Helper()
	var got []string
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if info.IsDir() {
			rel += "/"
		}
		got = append(got, rel)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	return got
}

func has(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// buildSrc lays down a small repo with tracked source, a .gitignore, ignored and
// junk directories, and a stand-in .git directory.
func buildSrc(t *testing.T) string {
	t.Helper()
	src := t.TempDir()
	write(t, filepath.Join(src, "main.go"), "package main")
	write(t, filepath.Join(src, "src", "app.ts"), "export {}")
	write(t, filepath.Join(src, ".gitignore"), "secrets.txt\nbuilt/\n")
	write(t, filepath.Join(src, "secrets.txt"), "shh")
	write(t, filepath.Join(src, "built", "out.js"), "// generated")
	write(t, filepath.Join(src, "node_modules", "left-pad", "index.js"), "module.exports={}")
	write(t, filepath.Join(src, "dist", "bundle.js"), "// bundle")
	write(t, filepath.Join(src, ".git", "HEAD"), "ref: refs/heads/main\n")
	write(t, filepath.Join(src, ".git", "config"), "[core]\n")
	return src
}

func TestCopySkipsJunkIgnoredAndGitByDefault(t *testing.T) {
	src := buildSrc(t)
	dst := filepath.Join(t.TempDir(), "copy")

	st, err := Copy(src, dst, false)
	if err != nil {
		t.Fatalf("Copy: %v", err)
	}
	if st.GitIncluded {
		t.Error("GitIncluded should be false without --git")
	}

	got := tree(t, dst)
	for _, want := range []string{"main.go", "src/", "src/app.ts", ".gitignore"} {
		if !has(got, want) {
			t.Errorf("expected %q in copy, got %v", want, got)
		}
	}
	for _, unwanted := range []string{"secrets.txt", "built/", "node_modules/", "dist/", ".git/"} {
		if has(got, unwanted) {
			t.Errorf("did not expect %q in copy, got %v", unwanted, got)
		}
	}
}

func TestCopyWithGitIncludesGitVerbatim(t *testing.T) {
	src := buildSrc(t)
	dst := filepath.Join(t.TempDir(), "copy")

	st, err := Copy(src, dst, true)
	if err != nil {
		t.Fatalf("Copy: %v", err)
	}
	if !st.GitIncluded {
		t.Error("GitIncluded should be true with --git")
	}

	got := tree(t, dst)
	for _, want := range []string{".git/", ".git/HEAD", ".git/config"} {
		if !has(got, want) {
			t.Errorf("expected %q in copy, got %v", want, got)
		}
	}
	// Junk is still pruned even with --git.
	if has(got, "node_modules/") {
		t.Errorf("node_modules should be skipped even with --git, got %v", got)
	}
}

func TestCopyPreservesExecutableBit(t *testing.T) {
	src := t.TempDir()
	script := filepath.Join(src, "run.sh")
	write(t, script, "#!/bin/sh\n")
	if err := os.Chmod(script, 0o755); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "copy")
	if _, err := Copy(src, dst, false); err != nil {
		t.Fatalf("Copy: %v", err)
	}
	info, err := os.Stat(filepath.Join(dst, "run.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o100 == 0 {
		t.Errorf("executable bit not preserved: mode %v", info.Mode())
	}
}

func TestCopyHandlesWorktreeGitPointerFile(t *testing.T) {
	// A linked worktree's .git is a FILE pointing into the parent repo, not a dir.
	src := t.TempDir()
	write(t, filepath.Join(src, "main.go"), "package main")
	write(t, filepath.Join(src, ".git"), "gitdir: /elsewhere/.git/worktrees/x\n")

	// Plain mode drops the pointer file entirely.
	dst := filepath.Join(t.TempDir(), "copy")
	if _, err := Copy(src, dst, false); err != nil {
		t.Fatalf("Copy: %v", err)
	}
	if has(tree(t, dst), ".git") {
		t.Error("worktree .git pointer file should be dropped in plain mode")
	}

	// --git mode refuses rather than copying a dangling pointer.
	if _, err := Copy(src, filepath.Join(t.TempDir(), "g"), true); err == nil {
		t.Error("--git should refuse a linked worktree's pointer file")
	}
}

func TestCopyRejectsDestInsideSource(t *testing.T) {
	src := t.TempDir()
	write(t, filepath.Join(src, "main.go"), "package main")
	if _, err := Copy(src, filepath.Join(src, "nested", "copy"), false); err == nil {
		t.Fatal("expected error copying into a path inside the source")
	}
}

func TestCopyRejectsSymlinkedDestInsideSource(t *testing.T) {
	src := t.TempDir()
	write(t, filepath.Join(src, "main.go"), "package main")
	// dst lives outside src lexically, but is a symlink pointing into src.
	dst := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(filepath.Join(src, "inner"), dst); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if _, err := Copy(src, dst, false); err == nil {
		t.Error("expected rejection: dst symlink resolves inside src")
	}
}

func TestCopyReportsStats(t *testing.T) {
	src := t.TempDir()
	write(t, filepath.Join(src, "a.txt"), "hello")     // 5 bytes
	write(t, filepath.Join(src, "sub", "b.txt"), "hi") // 2 bytes
	dst := filepath.Join(t.TempDir(), "copy")

	st, err := Copy(src, dst, false)
	if err != nil {
		t.Fatalf("Copy: %v", err)
	}
	if st.Files != 2 {
		t.Errorf("Files = %d, want 2", st.Files)
	}
	if st.Dirs != 1 {
		t.Errorf("Dirs = %d, want 1", st.Dirs)
	}
	if st.Bytes != 7 {
		t.Errorf("Bytes = %d, want 7", st.Bytes)
	}
}
