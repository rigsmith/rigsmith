package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rigsmith/cli/internal/detect"
)

func TestFileDeclaresMainPackage(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"plain main", "package main\n\nfunc main() {}\n", true},
		{"doc-commented main", "// Command rig does things.\n//\n// More docs.\npackage main\n", true},
		{"build-tagged main", "//go:build linux\n\npackage main\n", true},
		{"library", "// Package core ...\npackage core\n", false},
		{"main in a comment only", "// this mentions package main in prose\npackage util\n", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := fileDeclaresMainPackage(write("f.go", c.body)); got != c.want {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestGoDirHasMain(t *testing.T) {
	// A dir whose only main is in a _test.go file is not runnable.
	libWithTest := t.TempDir()
	if err := os.WriteFile(filepath.Join(libWithTest, "lib.go"), []byte("package lib\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(libWithTest, "x_test.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if goDirHasMain(libWithTest) {
		t.Fatal("a main package only in _test.go should not count as runnable")
	}

	// A dir with a real main package is runnable.
	cmd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cmd, "main.go"), []byte("package main\nfunc main(){}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !goDirHasMain(cmd) {
		t.Fatal("a dir with package main should be runnable")
	}

	// A dir with no .go files (e.g. a module whose code lives in subpackages).
	if goDirHasMain(t.TempDir()) {
		t.Fatal("an empty dir should not be runnable")
	}
}

func TestIsRunnable_GoFiltersLibrariesOthersPassThrough(t *testing.T) {
	lib := t.TempDir() // go module dir with no main
	if err := os.WriteFile(filepath.Join(lib, "x.go"), []byte("package lib\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if isRunnable(target{Eco: detect.Go, Dir: lib}) {
		t.Fatal("a Go library should not be runnable")
	}
	// Non-Go ecosystems pass through (their run mapping is the gate).
	if !isRunnable(target{Eco: detect.Node, Dir: lib}) {
		t.Fatal("non-Go targets should pass through as runnable")
	}
}
