package engine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rigsmith/rigsmith/core/pathmap"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/manifest"
)

func TestValidRestoreLinkPath(t *testing.T) {
	for _, p := range []string{"", ".", "..", "../outside", "a/../b", "/absolute", "//server/share", `C:\outside`, "C:outside", `a\b`, "a//b", "a/./b", "a/", "a:stream", "a\x00b", string([]byte{0xff})} {
		if validRestoreLinkPath(p) {
			t.Errorf("accepted nonportable endpoint %q", p)
		}
	}
	for _, p := range []string{"projects/main/memory", "..memory/notes", "shared notes", "projects/é/memory"} {
		if !validRestoreLinkPath(p) {
			t.Errorf("rejected local endpoint %q", p)
		}
	}
}

func requireRestoreSymlink(t *testing.T) {
	t.Helper()
	probe := t.TempDir()
	if err := os.Symlink(probe, filepath.Join(probe, "link")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
}

func TestRestoreLinksRejectsEscapingEndpoints(t *testing.T) {
	requireRestoreSymlink(t)
	for _, tc := range []struct {
		name, link, dest string
		slugs            map[string]string
	}{
		{"link traversal", "../outside/new/memory", "data", nil},
		{"target traversal", "new/memory", "../outside", nil},
		{"raw link before rewrite", "projects/../memory", "data", map[string]string{"..": "safe"}},
		{"raw target before rewrite", "new/memory", "projects/../data", map[string]string{"..": "safe"}},
		{"rewritten link", "projects/p/memory", "data", map[string]string{"p": "../../outside/new"}},
		{"rewritten target", "new/memory", "projects/p", map[string]string{"p": "../../outside"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent := t.TempDir()
			root, outside := filepath.Join(parent, "root"), filepath.Join(parent, "outside")
			write(t, root, "data/keep.txt", "inside")
			write(t, root, "projects/safe/data/keep.txt", "inside")
			write(t, outside, "keep.txt", "outside")
			if got := restoreLinks(root, map[string]string{tc.link: tc.dest}, tc.slugs); got != 0 {
				t.Errorf("restored escaping link: %d", got)
			}
			if entries, err := os.ReadDir(outside); err != nil || len(entries) != 1 {
				t.Fatalf("outside directory changed: %v %v", entries, err)
			}
			if _, err := os.Lstat(filepath.Join(root, "new")); !os.IsNotExist(err) {
				t.Error("created a parent for a rejected link", err)
			}
		})
	}
}

func TestRestoreLinksRejectsExternalTargetSymlinks(t *testing.T) {
	requireRestoreSymlink(t)
	for _, suffix := range []string{"", "/nested"} {
		t.Run(suffix, func(t *testing.T) {
			root, outside := t.TempDir(), t.TempDir()
			write(t, outside, "nested/keep.txt", "outside")
			if err := os.Symlink(outside, filepath.Join(root, "alias")); err != nil {
				t.Fatal(err)
			}
			if got := restoreLinks(root, map[string]string{"new/memory": "alias" + suffix}, nil); got != 0 {
				t.Error("restored link to external directory", got)
			}
			if _, err := os.Lstat(filepath.Join(root, "new")); !os.IsNotExist(err) {
				t.Error("created a parent for an external target", err)
			}
		})
	}
}

func TestRestoreLinksAcceptsChosenRootAndInternalTarget(t *testing.T) {
	requireRestoreSymlink(t)
	parent := t.TempDir()
	root, chosen := filepath.Join(parent, "actual"), filepath.Join(parent, "chosen")
	write(t, root, "data/keep.txt", "inside")
	if err := os.Symlink(root, chosen); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("data", filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	if got := restoreLinks(chosen, map[string]string{"projects/p/memory": "alias"}, nil); got != 1 {
		t.Fatal("valid internal link rejected", got)
	}
	if got := read(t, filepath.Join(chosen, "projects/p/memory/keep.txt")); got != "inside" {
		t.Fatal("restored link does not reach its target", got)
	}
}

func TestRestoreSkipsUnsafeSavedLinksAndRestoresSafeOnes(t *testing.T) {
	requireRestoreSymlink(t)
	staging, parent := t.TempDir(), t.TempDir()
	root, outside := filepath.Join(parent, "root"), filepath.Join(parent, "outside")
	write(t, staging, "cli/projects/main/s.jsonl", "transcript\n")
	write(t, staging, "cli/projects/main/memory/MEMORY.md", "facts")
	write(t, outside, "keep.txt", "outside")
	m := &manifest.Manifest{
		Schema: 1, SourceOS: pathmap.OSMacOS,
		Projects: map[string]manifest.Project{"main": {Cwd: "/main"}},
		Links: map[string]string{
			"projects/worktree/memory": "projects/main/memory",
			"../outside/new/memory":    "projects/main/memory",
			"projects/unsafe/memory":   "../outside",
		},
	}
	if err := m.Save(staging); err != nil {
		t.Fatal(err)
	}
	loaded, err := manifest.Load(staging)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := Restore(RestoreOptions{StagingDir: staging, Config: targetRootConfig(root), Manifest: loaded,
		Machine: config.Machine{Name: "fixture", OS: pathmap.OSMacOS, Home: "/Users/fixture"}, TargetOverride: override("cli", root)})
	if err != nil || len(rep.Roots) != 1 || rep.Roots[0].Links != 1 {
		t.Fatalf("restore did not skip only unsafe links: %+v %v", rep, err)
	}
	if got := read(t, filepath.Join(root, "projects/worktree/memory/MEMORY.md")); got != "facts" {
		t.Fatal(got)
	}
	if entries, err := os.ReadDir(outside); err != nil || len(entries) != 1 {
		t.Fatalf("restore escaped its root: %v %v", entries, err)
	}
	if _, err := os.Lstat(filepath.Join(root, "projects/unsafe")); !os.IsNotExist(err) {
		t.Fatal("created an unsafe link parent", err)
	}
}
