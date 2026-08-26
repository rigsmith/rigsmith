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
		t.Skipf("symlink unsupported: %v", err)
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
	// restoreLinks skips (by design) where symlinks can't be created, which
	// would fail the Links = 1 assertion — probe the capability first.
	probe := t.TempDir()
	if err := os.Symlink(probe, filepath.Join(probe, "link")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

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

// A stale 0-byte file left in staging by a pre-link-aware sync must never be
// written back over the live shared-memory symlink. It used to be copied with
// os.OpenFile(dst, O_WRONLY|…), which follows the link onto a directory and
// fails the whole restore with "open …/memory: is a directory".
func TestRestore_StagedFileNeverWritesThroughSymlink(t *testing.T) {
	staging := t.TempDir()
	write(t, staging, "cli/projects/-main/s.jsonl", "t\n")
	write(t, staging, "cli/projects/-main/memory/MEMORY.md", "facts")
	write(t, staging, "cli/projects/-wt/s.jsonl", "t\n")
	// the stale artefact: a link path staged as an empty regular file
	write(t, staging, "cli/projects/-wt/memory", "")

	target := t.TempDir()
	write(t, target, "projects/-main/memory/MEMORY.md", "facts")
	if err := os.MkdirAll(filepath.Join(target, "projects", "-wt"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(target, "projects", "-wt", "memory")
	if err := os.Symlink(filepath.Join(target, "projects", "-main", "memory"), link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	jane := config.Machine{Name: "jane", OS: pathmap.OSMacOS, Home: "/Users/jane"}
	rep, err := Restore(RestoreOptions{StagingDir: staging, Config: targetRootConfig(target), Machine: jane, TargetOverride: override("cli", target)})
	if err != nil {
		t.Fatalf("restore failed on a live shared-memory link: %v", err)
	}
	if rep.Roots[0].LinksKept != 1 {
		t.Errorf("LinksKept = %d, want 1", rep.Roots[0].LinksKept)
	}
	// the link survived as a link, still pointing at the shared dir
	info, err := os.Lstat(link)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("memory should still be a symlink, got %v (err %v)", info, err)
	}
	// and writing through it clobbered nothing
	if b, _ := os.ReadFile(filepath.Join(link, "MEMORY.md")); string(b) != "facts" {
		t.Errorf("shared memory through link = %q, want %q", b, "facts")
	}
}

// A directory symlink whose target sits outside the synced set is not
// recordable as a link, but it is still a directory — it must not be offered
// as a file. Any 0-byte placeholder an older sync staged for it is retired, so
// the repo digs itself out instead of handing the file back every restore.
func TestSync_RetiresStagedFileShadowedByDirLink(t *testing.T) {
	live := t.TempDir()
	write(t, live, "projects/-wt/s.jsonl",
		`{"type":"user","cwd":"/Users/john/Git/wt","isSidechain":false}`+"\n")
	outside := t.TempDir() // link target outside the root: not recordable
	if err := os.WriteFile(filepath.Join(outside, "MEMORY.md"), []byte("facts"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(live, "projects", "-wt", "memory")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	staging := t.TempDir()
	stale := filepath.Join(staging, "cli", "projects", "-wt", "memory")
	write(t, staging, "cli/projects/-wt/memory", "") // pre-link-aware residue

	m := config.Machine{Name: "mbp", OS: pathmap.OSMacOS, Home: "/Users/john"}
	if _, err := Sync(Options{StagingDir: staging, Config: cliOnlyConfig(live), Machine: m, SourceOverride: override("cli", live)}); err != nil {
		t.Fatalf("sync failed on an unrecordable dir link: %v", err)
	}
	if _, err := os.Lstat(stale); !os.IsNotExist(err) {
		t.Error("stale staged file shadowed by a dir link should be retired")
	}
}

// The guard must cover symlinked ANCESTORS, not just a symlinked leaf. Another
// machine that holds this project as a real directory stages
// projects/<slug>/memory/MEMORY.md; here that memory/ is a link, so writing the
// descendant follows it and clobbers the canonical project's memory — the same
// damage the leaf check prevents, one level up.
func TestRestore_NeverWritesThroughASymlinkedParent(t *testing.T) {
	staging := t.TempDir()
	write(t, staging, "cli/projects/-main/memory/MEMORY.md", "canonical facts")
	write(t, staging, "cli/projects/-wt/s.jsonl", "t\n")
	// staged as a real path by the machine that had it as a real directory
	write(t, staging, "cli/projects/-wt/memory/MEMORY.md", "worktree copy")

	target := t.TempDir()
	write(t, target, "projects/-main/memory/MEMORY.md", "canonical facts")
	if err := os.MkdirAll(filepath.Join(target, "projects", "-wt"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(target, "projects", "-wt", "memory")
	if err := os.Symlink(filepath.Join(target, "projects", "-main", "memory"), link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	jane := config.Machine{Name: "jane", OS: pathmap.OSMacOS, Home: "/Users/jane"}
	if _, err := Restore(RestoreOptions{StagingDir: staging, Config: targetRootConfig(target), Machine: jane, TargetOverride: override("cli", target)}); err != nil {
		t.Fatalf("restore: %v", err)
	}
	// The canonical memory must be untouched — writing through the link would
	// have replaced it with the worktree copy.
	got, _ := os.ReadFile(filepath.Join(target, "projects", "-main", "memory", "MEMORY.md"))
	if string(got) != "canonical facts" {
		t.Errorf("canonical memory clobbered through the link: got %q", got)
	}
}

// filepath.Rel returns "..memory" unchanged for a directory of that name, so a
// bare `..` prefix test would call it outside the root and skip the symlink
// check — writing through the very link the guard exists to protect.
func TestRestore_DotDotPrefixedDirIsStillGuarded(t *testing.T) {
	staging := t.TempDir()
	write(t, staging, "cli/projects/-main/memory/MEMORY.md", "canonical facts")
	write(t, staging, "cli/..memory/NOTES.md", "staged copy")

	target := t.TempDir()
	write(t, target, "projects/-main/memory/MEMORY.md", "canonical facts")
	link := filepath.Join(target, "..memory")
	if err := os.Symlink(filepath.Join(target, "projects", "-main", "memory"), link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	jane := config.Machine{Name: "jane", OS: pathmap.OSMacOS, Home: "/Users/jane"}
	if _, err := Restore(RestoreOptions{StagingDir: staging, Config: targetRootConfig(target), Machine: jane, TargetOverride: override("cli", target)}); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "projects", "-main", "memory", "NOTES.md")); err == nil {
		t.Error("wrote through a link under a '..'-prefixed directory")
	}
}

// --prune must never collect a symlink. `written` records the DESCENDANT path
// that was skipped, not the link itself, so judging the link by that map
// removes the machine's own state — the opposite of what the restore loop just
// went out of its way to preserve.
func TestRestore_PruneKeepsASymlinkedConfigDir(t *testing.T) {
	staging := t.TempDir()
	write(t, staging, "cli/skills/shared/SKILL.md", "shared skill")
	write(t, staging, "cli/skills/local/SKILL.md", "staged under the link")

	target := t.TempDir()
	write(t, target, "skills/shared/SKILL.md", "shared skill")
	link := filepath.Join(target, "skills", "local")
	if err := os.Symlink(filepath.Join(target, "skills", "shared"), link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	jane := config.Machine{Name: "jane", OS: pathmap.OSMacOS, Home: "/Users/jane"}
	if _, err := Restore(RestoreOptions{
		StagingDir: staging, Config: targetRootConfig(target), Machine: jane,
		TargetOverride: override("cli", target), Prune: true,
	}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Lstat(link)
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("prune removed the machine's own symlink: %v", err)
	}
}

// restoreLinks must apply the same ancestor rule as the write loop: checking
// only the leaf lets MkdirAll and Symlink follow a linked ancestor and create
// the link OUTSIDE the restore target.
func TestRestoreLinks_SkipsALinkUnderASymlinkedAncestor(t *testing.T) {
	staging := t.TempDir()
	write(t, staging, "cli/projects/-main/memory/MEMORY.md", "facts")
	write(t, staging, "cli/projects/-main/s.jsonl", "t\n")

	target := t.TempDir()
	outside := t.TempDir()
	write(t, target, "projects/-main/memory/MEMORY.md", "facts")
	// projects/-wt is a link OUT of the restore target
	if err := os.Symlink(outside, filepath.Join(target, "projects", "-wt")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	m := &manifest.Manifest{
		Schema: 1, SourceOS: pathmap.OSMacOS,
		Projects: map[string]manifest.Project{"-main": {Cwd: "/m"}, "-wt": {Cwd: "/w"}},
		Links:    map[string]string{"projects/-wt/memory": "projects/-main/memory"},
	}
	jane := config.Machine{Name: "jane", OS: pathmap.OSMacOS, Home: "/Users/jane"}
	if _, err := Restore(RestoreOptions{StagingDir: staging, Config: targetRootConfig(target),
		Machine: jane, Manifest: m, TargetOverride: override("cli", target)}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(outside, "memory")); err == nil {
		t.Error("a link was created outside the restore target")
	}
}
