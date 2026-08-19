package engine

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/rigsmith/rigsmith/core/pathmap"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/manifest"
)

// A worktree slug shares memory with its main project via a symlinked dir
// (memory -> <main-slug>/memory). Sync must not abort on it (it used to fail
// with "read …/memory: is a directory"), must sync the content once under the
// canonical slug, and must record the link in the manifest for restore.
func TestSync_RecordsSharedMemoryLink(t *testing.T) {
	live := t.TempDir()
	write(t, live, "projects/-Users-john-Git-grasp/s.jsonl",
		`{"type":"user","cwd":"/Users/john/Git/grasp","isSidechain":false}`+"\n")
	write(t, live, "projects/-Users-john-Git-grasp/memory/MEMORY.md", "facts")
	write(t, live, "projects/-Users-john-Git-grasp-wt/s.jsonl",
		`{"type":"user","cwd":"/Users/john/Git/grasp-wt","isSidechain":false}`+"\n")
	if err := os.Symlink(
		filepath.Join(live, "projects", "-Users-john-Git-grasp", "memory"),
		filepath.Join(live, "projects", "-Users-john-Git-grasp-wt", "memory")); err != nil {
		t.Fatal(err)
	}

	staging := t.TempDir()
	m := config.Machine{Name: "mbp", OS: pathmap.OSMacOS, Home: "/Users/john"}
	if _, err := Sync(Options{StagingDir: staging, Config: cliOnlyConfig(live), Machine: m, SourceOverride: override("cli", live)}); err != nil {
		t.Fatal(err)
	}

	// the link itself is not staged as a file
	if _, err := os.Lstat(filepath.Join(staging, "cli", "projects", "-Users-john-Git-grasp-wt", "memory")); !os.IsNotExist(err) {
		t.Error("memory link should not be staged")
	}
	man, err := manifest.Load(staging)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"projects/-Users-john-Git-grasp-wt/memory": "projects/-Users-john-Git-grasp/memory",
	}
	if !reflect.DeepEqual(man.Links, want) {
		t.Fatalf("manifest links = %v, want %v", man.Links, want)
	}
}

func TestRestore_RecreatesSharedMemoryLink(t *testing.T) {
	staging := t.TempDir()
	write(t, staging, "cli/projects/-Users-john-Git-grasp/s.jsonl", "t\n")
	write(t, staging, "cli/projects/-Users-john-Git-grasp/memory/MEMORY.md", "facts")
	write(t, staging, "cli/projects/-Users-john-Git-grasp-wt/s.jsonl", "t\n")
	m := &manifest.Manifest{
		Schema: 1, SourceOS: pathmap.OSMacOS,
		Projects: map[string]manifest.Project{
			"-Users-john-Git-grasp":    {Template: "$HOME/Git/grasp", Cwd: "/Users/john/Git/grasp"},
			"-Users-john-Git-grasp-wt": {Template: "$HOME/Git/grasp-wt", Cwd: "/Users/john/Git/grasp-wt"},
		},
		Links: map[string]string{
			"projects/-Users-john-Git-grasp-wt/memory": "projects/-Users-john-Git-grasp/memory",
		},
	}

	target := t.TempDir()
	jane := config.Machine{Name: "jane", OS: pathmap.OSMacOS, Home: "/Users/jane"}
	rep, err := Restore(RestoreOptions{StagingDir: staging, Config: targetRootConfig(target), Machine: jane, Manifest: m, TargetOverride: override("cli", target)})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Roots[0].Links != 1 {
		t.Fatalf("Links = %d, want 1", rep.Roots[0].Links)
	}

	// link recreated under jane's slugs, pointing at jane's target
	link := filepath.Join(target, "projects", "-Users-jane-Git-grasp-wt", "memory")
	dest, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("link not created: %v", err)
	}
	wantDest := filepath.Join(target, "projects", "-Users-jane-Git-grasp", "memory")
	if dest != wantDest {
		t.Errorf("link dest = %s, want %s", dest, wantDest)
	}
	// and it works: the shared MEMORY.md is reachable through it
	if read(t, filepath.Join(link, "MEMORY.md")) != "facts" {
		t.Error("shared memory not reachable through link")
	}
}

func TestRestore_LinkSkippedWhenTargetAbsentOrOccupied(t *testing.T) {
	staging := t.TempDir()
	write(t, staging, "cli/projects/-a/s.jsonl", "t\n")
	write(t, staging, "cli/projects/-b/s.jsonl", "t\n")
	write(t, staging, "cli/projects/-b/memory/MEMORY.md", "facts")
	m := &manifest.Manifest{
		Schema: 1, SourceOS: pathmap.OSMacOS,
		Projects: map[string]manifest.Project{"-a": {Cwd: "/a"}, "-b": {Cwd: "/b"}, "-c": {Cwd: "/c"}},
		Links: map[string]string{
			"projects/-a/memory": "projects/-c/memory", // target never restored
			"projects/-b/local":  "projects/-b/memory", // link path occupied locally
		},
	}
	target := t.TempDir()
	write(t, target, "projects/-b/local/own.md", "machine-local state")

	jane := config.Machine{Name: "jane", OS: pathmap.OSMacOS, Home: "/Users/jane"}
	rep, err := Restore(RestoreOptions{StagingDir: staging, Config: targetRootConfig(target), Machine: jane, Manifest: m, TargetOverride: override("cli", target)})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Roots[0].Links != 0 {
		t.Fatalf("Links = %d, want 0", rep.Roots[0].Links)
	}
	if _, err := os.Lstat(filepath.Join(target, "projects", "-a", "memory")); !os.IsNotExist(err) {
		t.Error("link with absent target should not be created")
	}
	// the occupied path kept the machine's own directory, not a link
	if info, err := os.Lstat(filepath.Join(target, "projects", "-b", "local")); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		t.Error("occupied path should keep the local dir")
	}
}
