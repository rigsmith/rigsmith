package walkutil

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// visit runs Walk and returns the visited files as root-relative '/' paths.
func visit(t *testing.T, root string) []string {
	t.Helper()
	var got []string
	err := Walk(root, func(p string, d fs.DirEntry) error {
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		got = append(got, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	sort.Strings(got)
	return got
}

func TestWalkSkipsJunkAndGitignored(t *testing.T) {
	root := t.TempDir()

	// Real files we expect to visit.
	writeFile(t, filepath.Join(root, "go.mod"), "module x\n")
	writeFile(t, filepath.Join(root, "src", "main.go"), "package main\n")

	// Default-skip dirs: must be pruned regardless of .gitignore.
	writeFile(t, filepath.Join(root, "node_modules", "dep", "package.json"), "{}")
	writeFile(t, filepath.Join(root, "vendor", "v.go"), "package v\n")

	// .gitignore-driven skips: a dir and a glob.
	writeFile(t, filepath.Join(root, ".gitignore"), "dist/\n*.tmp\n")
	writeFile(t, filepath.Join(root, "dist", "bundle.js"), "// built\n")
	writeFile(t, filepath.Join(root, "scratch.tmp"), "junk")
	writeFile(t, filepath.Join(root, "src", "cache.tmp"), "junk") // nested *.tmp

	got := visit(t, root)

	// .gitignore itself is a real file and is visited (git tracks it).
	want := []string{".gitignore", "go.mod", "src/main.go"}
	if len(got) != len(want) {
		t.Fatalf("visited %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("visited %v, want %v", got, want)
		}
	}
}

func TestWalkMissingRootIsNotError(t *testing.T) {
	root := filepath.Join(t.TempDir(), "does-not-exist")
	called := false
	err := Walk(root, func(string, fs.DirEntry) error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("missing root should not error, got %v", err)
	}
	if called {
		t.Fatal("fn should not be called for a missing root")
	}
}

func TestIgnorer(t *testing.T) {
	ign := parseIgnore(`
# comment
build
*.log
!keep.log
/rooted
cache/
src/generated
`)

	cases := []struct {
		name  string
		path  string
		isDir bool
		want  bool
	}{
		// bare name matches at any depth
		{"bare at root", "build", true, true},
		{"bare nested", "a/b/build", true, true},
		{"bare as file", "build", false, true},
		{"not the bare name", "builder", true, false},

		// *.ext glob, at any depth
		{"glob at root", "server.log", false, true},
		{"glob nested", "logs/app.log", false, true},
		{"glob non-match", "app.txt", false, false},

		// /rooted: anchored to root only
		{"rooted at root", "rooted", false, true},
		{"rooted nested does not match", "sub/rooted", false, false},

		// dir/ : directory-only
		{"dir-only matches dir", "cache", true, true},
		{"dir-only ignores file", "cache", false, false},

		// embedded slash: anchored full-path match
		{"embedded slash match", "src/generated", true, true},
		{"embedded slash non-anchored miss", "x/src/generated", true, false},

		// !negation re-includes (last match wins, negation comes after *.log)
		{"negation re-includes", "keep.log", false, false},
		{"negation only affects its match", "other.log", false, true},
	}
	for _, c := range cases {
		if got := ign.Ignored(c.path, c.isDir); got != c.want {
			t.Errorf("%s: Ignored(%q, dir=%v) = %v, want %v", c.name, c.path, c.isDir, got, c.want)
		}
	}
}

func TestNilIgnorerMatchesNothing(t *testing.T) {
	var ign *Ignorer
	if ign.Ignored("anything", false) {
		t.Error("nil Ignorer should match nothing")
	}
	if (&Ignorer{}).Ignored("anything", true) {
		t.Error("empty Ignorer should match nothing")
	}
}

// WalkReport prunes the extra directories a caller names — how `rig verify`
// keeps generated output from being counted as source.
func TestWalkReportSkipsNamedDirectories(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"src/a.js", "build/a.js", "out/nested/b.js"} {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	var seen []string
	unreadable, err := WalkReport(root, []string{"build", filepath.Join(root, "out")}, func(p string, _ fs.DirEntry) error {
		rel, _ := filepath.Rel(root, p)
		seen = append(seen, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatalf("WalkReport: %v", err)
	}
	if len(unreadable) != 0 {
		t.Errorf("unreadable = %v, want none", unreadable)
	}
	if len(seen) != 1 || seen[0] != "src/a.js" {
		t.Fatalf("visited %v, want only src/a.js (build/ and out/ pruned)", seen)
	}
}

// The whole point of WalkReport over Walk: a directory it could not descend into
// is handed back, so a caller comparing timestamps can decline to draw a
// conclusion instead of silently reporting on a partial scan.
func TestWalkReportNamesUnreadableDirectories(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory permissions don't gate traversal the same way on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root reads unreadable directories anyway")
	}
	root := t.TempDir()
	locked := filepath.Join(root, "locked")
	if err := os.MkdirAll(filepath.Join(locked, "inner"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "visible.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	var seen int
	unreadable, err := WalkReport(root, nil, func(string, fs.DirEntry) error {
		seen++
		return nil
	})
	if err != nil {
		t.Fatalf("WalkReport: %v", err)
	}
	if seen != 1 {
		t.Errorf("visited %d files, want the 1 readable one", seen)
	}
	if len(unreadable) != 1 || filepath.Base(unreadable[0]) != "locked" {
		t.Fatalf("unreadable = %v, want the locked directory named", unreadable)
	}

	// Walk keeps its forgiving contract: same tree, no error, no report.
	if err := Walk(root, func(string, fs.DirEntry) error { return nil }); err != nil {
		t.Errorf("Walk should still prune unreadable subtrees silently: %v", err)
	}
}
