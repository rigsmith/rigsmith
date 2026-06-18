package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/internal/rig/detect"
)

// writeDotnetWorkspace scaffolds a root whose only projects live in src/<name>,
// with no buildable primary at the root — so a bare verb has no single target.
func writeDotnetWorkspace(t *testing.T, root string, names ...string) {
	t.Helper()
	const csproj = `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><OutputType>Exe</OutputType><TargetFramework>net8.0</TargetFramework></PropertyGroup></Project>`
	for _, n := range names {
		dir := filepath.Join(root, "src", n)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, n+".csproj"), []byte(csproj), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// rebuild has no single argv, so it can't ride the generic picker — but at a
// workspace root with several packages and no primary it must still guide to a
// package (off a TTY: the helpful error), not abort on the missing primary.
func TestRebuildVerb_NoPrimaryOffersPackagesOffTTY(t *testing.T) {
	isolateGlobalConfig(t)
	root := t.TempDir()
	writeDotnetWorkspace(t, root, "App", "Tool") // two, so no lone target auto-runs
	t.Chdir(root)

	cmd := devVerbCmd("rebuild", "", false)
	cmd.SetContext(context.Background())
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.RunE(cmd, nil)
	if err == nil {
		t.Fatalf("want multi-package rebuild guidance, got nil (output: %q)", buf.String())
	}
	if strings.Contains(err.Error(), "no recognized ecosystem") {
		t.Fatalf("bare rebuild aborted on the missing primary instead of offering packages: %v", err)
	}
	if !strings.Contains(err.Error(), "workspace root with 2 packages") {
		t.Fatalf("err = %v, want the multi-package rebuild guidance", err)
	}
}

// An explicit project arg that names no workspace package is a clear error, not a
// silent rebuild of the wrong thing.
func TestRunRebuildVerb_UnknownProjectArg(t *testing.T) {
	isolateGlobalConfig(t)
	host, _ := newRunHost()
	err := runRebuildVerb(host, t.TempDir(), []string{"nope"}, false)
	if err == nil || !strings.Contains(err.Error(), "no workspace package") {
		t.Fatalf("err = %v, want a no-such-package error", err)
	}
}

// rebuildTasks enumerates every workspace package (each carries the ecosystem +
// dir runRebuild needs), keyed here by repo-relative path.
func TestRebuildTasks_ListsWorkspacePackages(t *testing.T) {
	isolateGlobalConfig(t)
	root := t.TempDir()
	writeDotnetWorkspace(t, root, "App", "Tool")

	host, _ := newRunHost()
	tasks := rebuildTasks(host, root)
	if len(tasks) != 2 {
		t.Fatalf("rebuildTasks = %d tasks, want 2", len(tasks))
	}
	rels := map[string]bool{}
	for _, tk := range tasks {
		rels[tk.rel] = true
		if tk.eco != detect.DotNet {
			t.Errorf("task %s eco = %q, want %q", tk.rel, tk.eco, detect.DotNet)
		}
	}
	if !rels["src/App"] || !rels["src/Tool"] {
		t.Fatalf("rebuildTasks rels = %v, want src/App and src/Tool", rels)
	}
}
