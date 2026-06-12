package walkutil

import (
	"io/fs"
	"os"
	"path/filepath"
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
