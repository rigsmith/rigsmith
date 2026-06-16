package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/internal/rig/detect"
)

// `rig run` completion suggests the expanded run targets (cmd/* binaries),
// matching how `rig run <name>` resolves — not the Go module name.
func TestRunTargetCompletion_SuggestsBinaries(t *testing.T) {
	isolateGlobalConfig(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"),
		[]byte("module example.com/app\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeGoPkg(t, root, "cmd/api", "main")
	writeGoPkg(t, root, "cmd/worker", "main")
	t.Chdir(root)

	cmd := devVerbCmd("run", "", false)
	cmd.SetContext(context.Background())
	names, _ := runTargetCompletion(cmd, nil, "")
	if !slices.Contains(names, "api") || !slices.Contains(names, "worker") {
		t.Fatalf("run completion = %v, want the cmd/* binaries api & worker", names)
	}
	if slices.Contains(names, "example.com/app") {
		t.Errorf("run completion should suggest binaries, not the module name: %v", names)
	}
}

// A repo with runnable projects only in subdirectories and no resolvable primary
// (no root marker) must still offer those projects on a bare `rig run` rather
// than aborting with "no recognized ecosystem" — the picker runs before the
// primary is required.
func TestRunVerb_NoPrimaryStillOffersSubprojects(t *testing.T) {
	isolateGlobalConfig(t)
	root := t.TempDir()
	csproj := `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><OutputType>Exe</OutputType><TargetFramework>net8.0</TargetFramework></PropertyGroup></Project>`
	for _, n := range []string{"App", "Tool"} { // two, so it can't auto-run a lone target
		dir := filepath.Join(root, "src", n)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, n+".csproj"), []byte(csproj), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(root)

	cmd := devVerbCmd("run", "", false)
	cmd.SetContext(context.Background())
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.RunE(cmd, nil)
	if err == nil {
		t.Fatalf("want the workspace picker's guidance, got nil (output: %q)", buf.String())
	}
	if strings.Contains(err.Error(), "no recognized ecosystem") {
		t.Fatalf("bare run aborted on the missing primary instead of offering subprojects: %v", err)
	}
	if !strings.Contains(err.Error(), "run target") {
		t.Fatalf("err = %v, want the multi-project run guidance", err)
	}
}

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
